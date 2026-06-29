package runtime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asjdf/goadb/internal/goadbserver/adbproto"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

func TestParseConfigAcceptsStartServerForm(t *testing.T) {
	t.Setenv("ADB_VENDOR_KEYS", "")

	cfg, err := parseConfig([]string{"-L", "tcp:localhost:5037", "start-server"}, "C:/Users/Any")
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.ListenAddr != "localhost:5037" || cfg.Mode != modeStartServer {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.KeyPath != "C:/Users/Any/.android/adbkey" {
		t.Fatalf("KeyPath = %q", cfg.KeyPath)
	}
}

func TestParseConfigAcceptsServerNodaemonForm(t *testing.T) {
	t.Setenv("ADB_VENDOR_KEYS", "")

	cfg, err := parseConfig([]string{"-L", "tcp:127.0.0.1:5038", "--key", "C:/k/adbkey", "--connect", "192.168.28.48:5555", "server", "nodaemon"}, "C:/Users/Any")
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:5038" || cfg.Mode != modeNoDaemon || cfg.KeyPath != "C:/k/adbkey" {
		t.Fatalf("config = %#v", cfg)
	}
	if !reflect.DeepEqual(cfg.InitialTCP, []string{"192.168.28.48:5555"}) {
		t.Fatalf("InitialTCP = %#v", cfg.InitialTCP)
	}
}

func TestParseConfigUsesADBVendorKeysWhenValid(t *testing.T) {
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "goadb.adb_key")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("ADB_VENDOR_KEYS", keyDir)

	cfg, err := parseConfig([]string{"-L", "tcp:localhost:5037", "start-server"}, "C:/Users/Any")
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.KeyPath != keyPath {
		t.Fatalf("KeyPath = %q, want %q", cfg.KeyPath, keyPath)
	}
}

func TestDLLSearchPathIncludesExeAndWorkingDirectories(t *testing.T) {
	got := dllSearchPath("", `C:\tools\adb`, `C:\repo`)
	want := `C:\tools\adb` + string(os.PathListSeparator) + `C:\repo`
	if got != want {
		t.Fatalf("dllSearchPath = %q, want %q", got, want)
	}

	got = dllSearchPath(`C:\repo`+string(os.PathListSeparator)+`C:\Windows`, `C:\tools\adb`, `C:\repo`)
	want = `C:\tools\adb` + string(os.PathListSeparator) + `C:\repo` + string(os.PathListSeparator) + `C:\Windows`
	if got != want {
		t.Fatalf("dllSearchPath with existing dirs = %q, want %q", got, want)
	}
}

func TestParseListenSpecRejectsUnsupportedAddress(t *testing.T) {
	if _, err := parseListenSpec("localfilesystem:/tmp/adb.sock"); err == nil {
		t.Fatal("parseListenSpec succeeded, want unsupported spec error")
	}
}

func TestResolveKeyPathAcceptsFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "custom")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := ResolveKeyPath(keyPath)

	if err != nil {
		t.Fatalf("ResolveKeyPath returned error: %v", err)
	}
	if got != keyPath {
		t.Fatalf("ResolveKeyPath = %q, want %q", got, keyPath)
	}
}

func TestResolveKeyPathUsesAdbKeyInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "adbkey")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := ResolveKeyPath(dir)

	if err != nil {
		t.Fatalf("ResolveKeyPath returned error: %v", err)
	}
	if got != keyPath {
		t.Fatalf("ResolveKeyPath = %q, want %q", got, keyPath)
	}
}

func TestResolveKeyPathUsesADBKeyExtensionInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "goadb.adb_key")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := ResolveKeyPath(dir)

	if err != nil {
		t.Fatalf("ResolveKeyPath returned error: %v", err)
	}
	if got != keyPath {
		t.Fatalf("ResolveKeyPath = %q, want %q", got, keyPath)
	}
}

func TestResolveKeyPathRejectsDirectoryWithoutKey(t *testing.T) {
	_, err := ResolveKeyPath(t.TempDir())

	if err == nil || !strings.Contains(err.Error(), "does not contain adbkey or *.adb_key") {
		t.Fatalf("ResolveKeyPath error = %v, want missing key message", err)
	}
}

func TestStartBackgroundListensBeforeUSBScanCompletes(t *testing.T) {
	block := make(chan struct{})
	originalUSBEnumerators := usbEnumerators
	usbEnumerators = func() []namedUSBEnumerator {
		return []namedUSBEnumerator{{
			name:       "blocking",
			enumerator: blockingUSBEnumerator{block: block},
		}}
	}
	t.Cleanup(func() {
		close(block)
		usbEnumerators = originalUSBEnumerators
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	address := net.JoinHostPort("localhost", strconv.Itoa(freeTCPPort(t)))
	if err := StartBackground(ctx, Options{
		ListenAddr: address,
		KeyPath:    writeRuntimeTestKeyPair(t),
	}); err != nil {
		t.Fatalf("StartBackground returned error: %v", err)
	}

	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("DialTimeout(%s) returned error: %v", address, err)
	}
	_ = conn.Close()
}

func TestCompositeOpenerScanUSBAcceptsNeutralEnumerator(t *testing.T) {
	registry := transport.NewRegistry()
	opener := newCompositeOpener(nil)
	backend := &fakeUSBEnumerator{
		devices: []usbDevice{{
			Name:   `\\?\usb#vid_18d1&pid_4ee7#ABC123#{f72fe0d4-cbcb-407d-8814-9ed673d0dd6b}`,
			Serial: "ABC123",
			Conn:   adbDeviceHandshakeConn(t),
		}},
	}

	if err := opener.ScanUSB(context.Background(), backend, registry, nil); err != nil {
		t.Fatalf("ScanUSB returned error: %v", err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("registry has %d devices, want 1", len(snapshot))
	}
	info := snapshot[0]
	if info.Serial != "ABC123" || info.Type != transport.TypeUSB || info.DevPath == "" {
		t.Fatalf("registry info = %#v", info)
	}
}

func TestCompositeOpenerScanUSBSkipsDeviceWhoseHandshakeBlocks(t *testing.T) {
	originalTimeout := usbConnectTimeout
	usbConnectTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		usbConnectTimeout = originalTimeout
	})

	registry := transport.NewRegistry()
	opener := newCompositeOpener(nil)
	host, device := net.Pipe()
	t.Cleanup(func() {
		_ = host.Close()
		_ = device.Close()
	})
	backend := &fakeUSBEnumerator{
		devices: []usbDevice{{
			Name:   "blocked",
			Serial: "BLOCKED",
			Conn:   host,
		}},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- opener.ScanUSB(context.Background(), backend, registry, nil)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ScanUSB returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ScanUSB did not return when USB handshake blocked")
	}
	if snapshot := registry.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("registry has %d devices, want blocked device skipped", len(snapshot))
	}
}

type fakeUSBEnumerator struct {
	devices []usbDevice
}

func (f *fakeUSBEnumerator) Enumerate() ([]usbDevice, error) {
	return f.devices, nil
}

type blockingUSBEnumerator struct {
	block <-chan struct{}
}

func (e blockingUSBEnumerator) Enumerate() ([]usbDevice, error) {
	<-e.block
	return nil, nil
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen localhost:0 returned error: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeRuntimeTestKeyPair(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "adbkey")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("WriteFile private key returned error: %v", err)
	}
	if err := os.WriteFile(privatePath+".pub", []byte("runtime test public key"), 0o600); err != nil {
		t.Fatalf("WriteFile public key returned error: %v", err)
	}
	return privatePath
}

func adbDeviceHandshakeConn(t *testing.T) net.Conn {
	t.Helper()
	host, device := net.Pipe()
	t.Cleanup(func() {
		_ = host.Close()
		_ = device.Close()
	})
	go func() {
		packet, err := adbproto.ReadPacket(device, adbproto.MaxPayload)
		if err != nil {
			return
		}
		if packet.Command != adbproto.CmdCNXN {
			return
		}
		_ = adbproto.WritePacket(device, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::ro.product.name=test;ro.product.model=Model;ro.product.device=device;features=shell_v2"),
		})
	}()
	return host
}
