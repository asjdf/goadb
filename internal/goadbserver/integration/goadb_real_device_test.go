//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	adb "github.com/asjdf/goadb"
)

func TestRealDevicesThroughGoServer(t *testing.T) {
	if os.Getenv("GOADB_REAL_DEVICE_TESTS") != "1" {
		t.Skip("set GOADB_REAL_DEVICE_TESTS=1 to run real-device integration tests")
	}

	serverPath := os.Getenv("GOADB_SERVER_BIN")
	if serverPath == "" {
		t.Fatal("set GOADB_SERVER_BIN to the built goadb-server.exe")
	}
	tcpAddr := getenvDefault("GOADB_TCP_DEVICE", "192.168.28.48:5555")
	host := getenvDefault("GOADB_SERVER_HOST", "localhost")
	port := getenvIntDefault(t, "GOADB_SERVER_PORT", 5037)

	client, err := adb.NewWithConfig(adb.ServerConfig{
		PathToAdb: serverPath,
		Host:      host,
		Port:      port,
		KeyPath:   goadbKeyDir(t),
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	if _, err := client.ServerVersion(); err != nil {
		t.Fatalf("ServerVersion returned error: %v", err)
	}
	if err := connectTCP(client, tcpAddr); err != nil {
		t.Fatalf("Connect(%s) returned error: %v", tcpAddr, err)
	}

	devices := waitForUSBAndTCPDevices(t, client, tcpAddr, 15*time.Second)
	serials, err := client.ListDeviceSerials()
	if err != nil {
		t.Fatalf("ListDeviceSerials returned error: %v", err)
	}
	for _, info := range devices {
		if !containsString(serials, info.Serial) {
			t.Fatalf("ListDeviceSerials did not include %s: %#v", info.Serial, serials)
		}
	}
	usbDevice, err := client.AnyOnlineUsbDevice()
	if err != nil {
		t.Fatalf("AnyOnlineUsbDevice returned error: %v", err)
	}
	usbSerial, err := usbDevice.Serial()
	if err != nil {
		t.Fatalf("AnyOnlineUsbDevice.Serial returned error: %v", err)
	}
	if usbSerial == tcpAddr {
		t.Fatalf("AnyOnlineUsbDevice returned TCP serial %s", usbSerial)
	}

	watcher := client.NewDeviceWatcher()
	defer watcher.Shutdown()
	waitForWatcherOnline(t, watcher, []string{tcpAddr, usbSerial}, 5*time.Second)

	for _, info := range devices {
		device, err := client.DeviceBySerial(info.Serial, true)
		if err != nil {
			t.Fatalf("%s DeviceBySerial returned error: %v", info.Serial, err)
		}
		serial, err := device.Serial()
		if err != nil {
			t.Fatalf("%s Serial returned error: %v", info.Serial, err)
		}
		if serial != info.Serial {
			t.Fatalf("%s Serial = %q", info.Serial, serial)
		}
		if _, err := device.DevicePath(); err != nil {
			t.Fatalf("%s DevicePath returned error: %v", info.Serial, err)
		}
		state, err := device.State()
		if err != nil {
			t.Fatalf("%s State returned error: %v", info.Serial, err)
		}
		if state != adb.StateOnline {
			t.Fatalf("%s State = %s, want online", info.Serial, state)
		}
		deviceInfo, err := device.DeviceInfo()
		if err != nil {
			t.Fatalf("%s DeviceInfo returned error: %v", info.Serial, err)
		}
		if deviceInfo.Serial != info.Serial {
			t.Fatalf("%s DeviceInfo serial = %q", info.Serial, deviceInfo.Serial)
		}
		if features, err := device.FeatureList(); err != nil {
			t.Fatalf("%s FeatureList returned error: %v", info.Serial, err)
		} else if strings.TrimSpace(features) == "" {
			t.Fatalf("%s FeatureList returned empty features", info.Serial)
		}

		output, err := device.RunCommand("echo", "goadb-server")
		if err != nil {
			t.Fatalf("%s RunCommand returned error: %v", info.Serial, err)
		}
		if !strings.Contains(output, "goadb-server") {
			t.Fatalf("%s RunCommand output = %q", info.Serial, output)
		}

		if _, err := device.Stat("/sdcard"); err != nil {
			t.Fatalf("%s Stat(/sdcard) returned error: %v", info.Serial, err)
		}
		entries, err := device.ListDirEntries("/sdcard")
		if err != nil {
			t.Fatalf("%s ListDirEntries(/sdcard) returned error: %v", info.Serial, err)
		}
		if _, err := entries.ReadAll(); err != nil {
			t.Fatalf("%s ReadAll(/sdcard) returned error: %v", info.Serial, err)
		}

		remoteReadPath := "/sdcard/goadb_server_read_test.txt"
		if _, err := device.RunCommand("sh", "-c", "echo goadb-server > "+remoteReadPath); err != nil {
			t.Fatalf("%s create read test file returned error: %v", info.Serial, err)
		}
		reader, err := device.OpenRead(remoteReadPath)
		if err != nil {
			t.Fatalf("%s OpenRead(%s) returned error:\n%s", info.Serial, remoteReadPath, adb.ErrorWithCauseChain(err))
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(reader); err != nil {
			t.Fatalf("%s ReadFrom(%s) returned error: %v", info.Serial, remoteReadPath, err)
		}
		_ = reader.Close()
		if !strings.Contains(buf.String(), "goadb-server") {
			t.Fatalf("%s read test file = %q, want marker", info.Serial, buf.String())
		}

		remoteWritePath := "/sdcard/goadb_server_write_test.txt"
		writer, err := device.OpenWrite(remoteWritePath, 0o644, adb.MtimeOfClose)
		if err != nil {
			t.Fatalf("%s OpenWrite(%s) returned error:\n%s", info.Serial, remoteWritePath, adb.ErrorWithCauseChain(err))
		}
		if _, err := writer.Write([]byte("written-through-sync\n")); err != nil {
			t.Fatalf("%s writer.Write returned error: %v", info.Serial, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("%s writer.Close returned error: %v", info.Serial, err)
		}
		output, err = device.RunCommand("cat", remoteWritePath)
		if err != nil {
			t.Fatalf("%s cat write test returned error: %v", info.Serial, err)
		}
		if !strings.Contains(output, "written-through-sync") {
			t.Fatalf("%s cat write test output = %q", info.Serial, output)
		}

		shell, err := device.InteractiveShell("cat")
		if err != nil {
			t.Fatalf("%s InteractiveShell returned error: %v", info.Serial, err)
		}
		if _, err := shell.WriteLine("interactive-goadb-server"); err != nil {
			_ = shell.Close()
			t.Fatalf("%s shell.WriteLine returned error: %v", info.Serial, err)
		}
		line, err := shell.ReadLine()
		_ = shell.Close()
		if err != nil {
			t.Fatalf("%s shell.ReadLine returned error: %v", info.Serial, err)
		}
		if !strings.Contains(line, "interactive-goadb-server") {
			t.Fatalf("%s shell line = %q", info.Serial, line)
		}
		if _, err := device.Remount(); err != nil {
			t.Fatalf("%s Remount returned transport error: %v", info.Serial, err)
		}

		localPort := freeTCPPort(t)
		if err := device.StartPortForwarding(adb.ForwardProtocolKindTCP, localPort, adb.ForwardProtocolKindTCP, 5555); err != nil {
			t.Fatalf("%s StartPortForwarding returned error: %v", info.Serial, err)
		}
		forwards, err := client.ForwardList()
		if err != nil {
			t.Fatalf("ForwardList returned error: %v", err)
		}
		if !containsForward(forwards, info.Serial, localPort) {
			t.Fatalf("ForwardList did not contain %s tcp:%d: %#v", info.Serial, localPort, forwards)
		}
		if err := device.RemovePortForwarding(adb.ForwardProtocolKindTCP, localPort); err != nil {
			t.Fatalf("%s RemovePortForwarding returned error: %v", info.Serial, err)
		}
		if err := device.RemoveAllPortForwards(); err != nil {
			t.Fatalf("%s RemoveAllPortForwards returned error: %v", info.Serial, err)
		}
	}

	if err := client.Disconnect(tcpAddr); err != nil {
		t.Fatalf("Disconnect(%s) returned error: %v", tcpAddr, err)
	}
	waitForWatcherTransition(t, watcher, tcpAddr, func(event adb.DeviceStateChangedEvent) bool {
		return event.WentOffline()
	}, 5*time.Second)
	if err := connectTCP(client, tcpAddr); err != nil {
		t.Fatalf("reconnect %s returned error: %v", tcpAddr, err)
	}
	waitForWatcherTransition(t, watcher, tcpAddr, func(event adb.DeviceStateChangedEvent) bool {
		return event.CameOnline()
	}, 5*time.Second)

	apkPath := os.Getenv("GOADB_TEST_APK")
	if apkPath != "" {
		apkData, err := os.ReadFile(filepath.Clean(apkPath))
		if err != nil {
			t.Fatalf("ReadFile(%s) returned error: %v", apkPath, err)
		}
		for _, info := range devices {
			device := client.Device(adb.DeviceWithSerial(info.Serial))
			if err := device.Install(bytes.NewReader(apkData), "-r", "-t"); err != nil {
				t.Fatalf("%s Install returned error: %v", info.Serial, err)
			}
			verifyInstallCancellation(t, device, info.Serial, apkData)
		}
	}
}

func verifyInstallCancellation(t *testing.T, device *adb.Device, serial string, apkData []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	apk := newSlowLenReader(apkData, 1, 5*time.Millisecond)
	errCh := make(chan error, 1)
	go func() {
		errCh <- device.InstallWithContext(ctx, apk, "-r", "-t")
	}()
	select {
	case <-apk.started():
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("%s InstallWithContext did not start reading APK", serial)
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("%s InstallWithContext succeeded after cancellation", serial)
		}
		if !strings.Contains(adb.ErrorWithCauseChain(err), "install canceled") {
			t.Fatalf("%s InstallWithContext error = %s, want install canceled", serial, adb.ErrorWithCauseChain(err))
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s InstallWithContext did not return after cancellation", serial)
	}
}

type slowLenReader struct {
	data      []byte
	chunkSize int
	delay     time.Duration

	offset    int
	startedCh chan struct{}
	startedOk bool
}

func newSlowLenReader(data []byte, chunkSize int, delay time.Duration) *slowLenReader {
	return &slowLenReader{data: data, chunkSize: chunkSize, delay: delay, startedCh: make(chan struct{})}
}

func (r *slowLenReader) Len() int {
	return len(r.data)
}

func (r *slowLenReader) Read(p []byte) (int, error) {
	if !r.startedOk {
		close(r.startedCh)
		r.startedOk = true
	}
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	n := len(p)
	if n > r.chunkSize {
		n = r.chunkSize
	}
	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}
	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

func (r *slowLenReader) started() <-chan struct{} {
	return r.startedCh
}

func waitForUSBAndTCPDevices(t *testing.T, client *adb.Adb, tcpAddr string, timeout time.Duration) []*adb.DeviceInfo {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastDevices []*adb.DeviceInfo
	var lastErr error
	for time.Now().Before(deadline) {
		devices, err := client.ListDevices()
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		lastDevices = devices
		foundTCP := false
		foundUSB := false
		for _, info := range devices {
			if info.Serial == tcpAddr {
				foundTCP = true
			}
			if info.IsUsb() {
				foundUSB = true
			}
		}
		if foundTCP && foundUSB {
			return devices
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("ListDevices did not become ready: %v", lastErr)
	}
	t.Fatalf("ListDevices did not include both TCP device %s and a USB device within %s: %#v", tcpAddr, timeout, lastDevices)
	return nil
}

func waitForWatcherOnline(t *testing.T, watcher *adb.DeviceWatcher, serials []string, timeout time.Duration) {
	t.Helper()
	want := make(map[string]bool, len(serials))
	for _, serial := range serials {
		want[serial] = true
	}
	waitForWatcherTransition(t, watcher, "", func(event adb.DeviceStateChangedEvent) bool {
		if event.CameOnline() {
			delete(want, event.Serial)
		}
		return len(want) == 0
	}, timeout)
}

func waitForWatcherTransition(t *testing.T, watcher *adb.DeviceWatcher, serial string, match func(adb.DeviceStateChangedEvent) bool, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-watcher.C():
			if !ok {
				t.Fatalf("watcher closed before expected transition; err=%v", watcher.Err())
			}
			if serial != "" && event.Serial != serial {
				continue
			}
			if match(event) {
				return
			}
		case <-timer.C:
			if err := watcher.Err(); err != nil {
				t.Fatalf("watcher error before expected transition: %v", err)
			}
			t.Fatalf("timed out waiting for watcher transition for %q", serial)
		}
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen free port returned error: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func containsForward(forwards []adb.Forward, serial string, localPort int) bool {
	for _, fwd := range forwards {
		if fwd.Serial == serial && fwd.LocalProtocolKind == adb.ForwardProtocolKindTCP && fwd.LocalPort == localPort {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func connectTCP(client *adb.Adb, address string) error {
	host, portRaw, ok := strings.Cut(address, ":")
	if !ok {
		return client.Connect(address, 5555)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return err
	}
	return client.Connect(host, port)
}

func getenvDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvIntDefault(t *testing.T, key string, fallback int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", key, value, err)
	}
	return parsed
}
