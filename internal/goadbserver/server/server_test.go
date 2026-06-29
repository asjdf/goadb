package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/asjdf/goadb/internal/goadbserver/forward"
	"github.com/asjdf/goadb/internal/goadbserver/smartsocket"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

func TestHostVersionDevicesAndDeviceAttributes(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{
		Serial:   "USB123",
		Type:     transport.TypeUSB,
		State:    transport.StateDevice,
		DevPath:  "1-1",
		Product:  "pixel",
		Model:    "Pixel_8",
		Device:   "shiba",
		Features: []string{"shell_v2", "cmd"},
	})
	srv := New(registry, Options{})

	if got := roundTrip(t, srv, "host:version"); got != "0029" {
		t.Fatalf("host:version = %q, want 0029", got)
	}
	if got := roundTrip(t, srv, "host:goadb-server"); got != "goadb-server" {
		t.Fatalf("host:goadb-server = %q, want marker", got)
	}
	if got := roundTrip(t, srv, "host:devices"); got != "USB123\tdevice\n" {
		t.Fatalf("host:devices = %q", got)
	}
	if got := roundTrip(t, srv, "host:devices-l"); got != "USB123 device usb:1-1 product:pixel model:Pixel_8 device:shiba transport_id:1\n" {
		t.Fatalf("host:devices-l = %q", got)
	}
	if got := roundTrip(t, srv, "host-serial:USB123:get-state"); got != "device" {
		t.Fatalf("get-state = %q, want device", got)
	}
	if got := roundTrip(t, srv, "host-serial:USB123:get-serialno"); got != "USB123" {
		t.Fatalf("get-serialno = %q, want USB123", got)
	}
	if got := roundTrip(t, srv, "host-serial:USB123:get-devpath"); got != "1-1" {
		t.Fatalf("get-devpath = %q, want 1-1", got)
	}
	if got := roundTrip(t, srv, "host-serial:USB123:features"); got != "shell_v2,cmd" {
		t.Fatalf("features = %q, want shell_v2,cmd", got)
	}
}

func TestTrackDevicesSendsInitialAndUpdatedSnapshots(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice})
	srv := New(registry, Options{})

	client, done := servePipe(t, srv)

	if err := smartsocket.WriteProtocolString(client, "host:track-devices"); err != nil {
		t.Fatalf("WriteProtocolString returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("status = %q, want OKAY", status)
	}
	if got, err := smartsocket.ReadProtocolString(client); err != nil || got != "USB123\tdevice\n" {
		t.Fatalf("initial track snapshot = %q, %v", got, err)
	}

	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateOffline})
	if got, err := smartsocket.ReadProtocolString(client); err != nil || got != "USB123\toffline\n" {
		t.Fatalf("updated track snapshot = %q, %v", got, err)
	}

	client.Close()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice})
	waitDone(t, done)
}

func TestConnectAndDisconnectTCPTransport(t *testing.T) {
	registry := transport.NewRegistry()
	srv := New(registry, Options{
		Connector: ConnectorFunc(func(ctx context.Context, address string) (transport.Info, error) {
			if address != "192.168.28.48:5555" {
				t.Fatalf("connector address = %q", address)
			}
			return transport.Info{Serial: address, Type: transport.TypeTCP, State: transport.StateDevice}, nil
		}),
	})

	if got := roundTrip(t, srv, "host:connect:192.168.28.48:5555"); got != "connected to 192.168.28.48:5555" {
		t.Fatalf("connect response = %q", got)
	}
	if got := registry.ShortList(); got != "192.168.28.48:5555\tdevice\n" {
		t.Fatalf("registry after connect = %q", got)
	}
	if got := roundTrip(t, srv, "host-serial:192.168.28.48:5555:get-state"); got != "device" {
		t.Fatalf("tcp serial get-state = %q, want device", got)
	}
	if got := roundTrip(t, srv, "host:disconnect:192.168.28.48:5555"); got != "disconnected 192.168.28.48:5555" {
		t.Fatalf("disconnect response = %q", got)
	}
	if got := registry.ShortList(); got != "" {
		t.Fatalf("registry after disconnect = %q", got)
	}
}

func TestKillRequestInvokesShutdownAfterOKAY(t *testing.T) {
	registry := transport.NewRegistry()
	shutdown := make(chan struct{}, 1)
	srv := New(registry, Options{
		Shutdown: func() {
			shutdown <- struct{}{}
		},
	})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:kill"); err != nil {
		t.Fatalf("WriteProtocolString returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("status = %q, want OKAY", status)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback was not invoked")
	}
}

func TestTransportSwitchThenFeatures(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice, Features: []string{"shell_v2", "cmd"}})
	srv := New(registry, Options{})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:transport:USB123"); err != nil {
		t.Fatalf("transport write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("transport status = %q, want OKAY", status)
	}
	if err := smartsocket.WriteProtocolString(client, "host:features"); err != nil {
		t.Fatalf("features write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("features status = %q, want OKAY", status)
	}
	if got, err := smartsocket.ReadProtocolString(client); err != nil || got != "shell_v2,cmd" {
		t.Fatalf("features = %q, %v; want shell_v2,cmd", got, err)
	}
}

func TestTransportSwitchThenShellServiceIsProxied(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice})
	remoteHost, remoteDevice := net.Pipe()
	defer remoteDevice.Close()
	opened := make(chan string, 1)
	srv := New(registry, Options{
		Opener: forward.OpenerFunc(func(ctx context.Context, serial, service string) (io.ReadWriteCloser, error) {
			opened <- serial + " " + service
			return remoteHost, nil
		}),
	})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:transport:USB123"); err != nil {
		t.Fatalf("transport write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("transport status = %q, want OKAY", status)
	}
	if err := smartsocket.WriteProtocolString(client, "shell:id"); err != nil {
		t.Fatalf("shell write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("shell status = %q, want OKAY", status)
	}
	if got := <-opened; got != "USB123 shell:id" {
		t.Fatalf("opener got %q, want USB123 shell:id", got)
	}
	if _, err := remoteDevice.Write([]byte("uid=0\n")); err != nil {
		t.Fatalf("remote write returned error: %v", err)
	}
	remoteDevice.Close()
	data, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "uid=0\n" {
		t.Fatalf("proxied data = %q, want uid output", string(data))
	}
}

func TestTportSerialSwitchThenShellServiceIsProxied(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "192.168.28.74:5555", Type: transport.TypeTCP, State: transport.StateDevice})
	remoteHost, remoteDevice := net.Pipe()
	defer remoteDevice.Close()
	opened := make(chan string, 1)
	srv := New(registry, Options{
		Opener: forward.OpenerFunc(func(ctx context.Context, serial, service string) (io.ReadWriteCloser, error) {
			opened <- serial + " " + service
			return remoteHost, nil
		}),
	})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:tport:serial:192.168.28.74:5555"); err != nil {
		t.Fatalf("tport write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("tport status = %q, want OKAY", status)
	}
	transportIDRaw := make([]byte, 8)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	if _, err := io.ReadFull(client, transportIDRaw); err != nil {
		t.Fatalf("transport id read returned error: %v", err)
	}
	if err := client.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline returned error: %v", err)
	}
	if got := binary.LittleEndian.Uint64(transportIDRaw); got != 1 {
		t.Fatalf("transport id = %d, want 1", got)
	}
	if err := smartsocket.WriteProtocolString(client, "shell:id"); err != nil {
		t.Fatalf("shell write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("shell status = %q, want OKAY", status)
	}
	if got := <-opened; got != "192.168.28.74:5555 shell:id" {
		t.Fatalf("opener got %q, want TCP serial shell:id", got)
	}
}

func TestBenignProxyCloseErrorsAreIgnored(t *testing.T) {
	if !isBenignProxyCloseError(io.EOF) {
		t.Fatal("io.EOF was not treated as benign")
	}
	if !isBenignProxyCloseError(net.ErrClosed) {
		t.Fatal("net.ErrClosed was not treated as benign")
	}
	if !isBenignProxyCloseError(errors.New("writeto tcp 127.0.0.1:5037->127.0.0.1:49170: read tcp 127.0.0.1:5037->127.0.0.1:49170: use of closed network connection")) {
		t.Fatal("normal closed network connection copy error was not treated as benign")
	}
	if isBenignProxyCloseError(errors.New("remote auth failed")) {
		t.Fatal("unexpected proxy error was treated as benign")
	}
}

func TestTransportRemountReturnsLengthPrefixedSingleResponse(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice})
	opened := make(chan string, 1)
	srv := New(registry, Options{
		Opener: forward.OpenerFunc(func(ctx context.Context, serial, service string) (io.ReadWriteCloser, error) {
			opened <- serial + " " + service
			return &scriptedStream{Reader: bytes.NewReader([]byte("remount output\n"))}, nil
		}),
	})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:transport:USB123"); err != nil {
		t.Fatalf("transport write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("transport status = %q, want OKAY", status)
	}
	if err := smartsocket.WriteProtocolString(client, "remount"); err != nil {
		t.Fatalf("remount write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("remount status = %q, want OKAY", status)
	}
	if got, err := smartsocket.ReadProtocolString(client); err != nil || got != "remount output\n" {
		t.Fatalf("remount message = %q, %v; want length-prefixed output", got, err)
	}
	if got := <-opened; got != "USB123 remount:" {
		t.Fatalf("opener got %q, want USB123 remount:", got)
	}
}

func TestTransportForwardRequestsUseForwardManager(t *testing.T) {
	registry := transport.NewRegistry()
	registry.Upsert(transport.Info{Serial: "USB123", Type: transport.TypeUSB, State: transport.StateDevice})
	manager := forward.New(nil)
	defer manager.RemoveAll()
	srv := New(registry, Options{ForwardManager: manager})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:transport:USB123"); err != nil {
		t.Fatalf("transport write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("transport status = %q, want OKAY", status)
	}
	if err := smartsocket.WriteProtocolString(client, "host:forward:tcp:0;tcp:9000"); err != nil {
		t.Fatalf("forward write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("forward status = %q, want OKAY", status)
	}

	list := roundTrip(t, srv, "host:list-forward")
	if !strings.Contains(list, "USB123 tcp:") || !strings.Contains(list, " tcp:9000\n") {
		t.Fatalf("host:list-forward = %q", list)
	}
}

func TestUnsupportedRequestReturnsFAIL(t *testing.T) {
	registry := transport.NewRegistry()
	srv := New(registry, Options{})

	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, "host:unknown"); err != nil {
		t.Fatalf("request write returned error: %v", err)
	}
	if status := readStatus(t, client); status != "FAIL" {
		t.Fatalf("status = %q, want FAIL", status)
	}
	msg, err := smartsocket.ReadProtocolString(client)
	if err != nil {
		t.Fatalf("ReadProtocolString returned error: %v", err)
	}
	if !strings.Contains(msg, "unsupported") {
		t.Fatalf("FAIL message = %q, want unsupported detail", msg)
	}
}

type scriptedStream struct {
	*bytes.Reader
}

func (s *scriptedStream) Write(p []byte) (int, error) {
	return len(p), nil
}

func (s *scriptedStream) Close() error {
	return nil
}

func roundTrip(t *testing.T, srv *Server, request string) string {
	t.Helper()
	client, done := servePipe(t, srv)
	defer waitDone(t, done)
	defer client.Close()

	if err := smartsocket.WriteProtocolString(client, request); err != nil {
		t.Fatalf("WriteProtocolString returned error: %v", err)
	}
	if status := readStatus(t, client); status != "OKAY" {
		t.Fatalf("status = %q, want OKAY", status)
	}
	msg, err := smartsocket.ReadProtocolString(client)
	if err != nil {
		t.Fatalf("ReadProtocolString returned error: %v", err)
	}
	return msg
}

func servePipe(t *testing.T, srv *Server) (net.Conn, <-chan error) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- srv.ServeConn(context.Background(), server)
	}()
	return client, done
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && err != io.EOF && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("ServeConn returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ServeConn")
	}
}

func readStatus(t *testing.T, r io.Reader) string {
	t.Helper()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("ReadFull status returned error: %v", err)
	}
	return string(buf)
}
