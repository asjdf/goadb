package transport

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/asjdf/goadb/internal/goadbserver/adbproto"
	"github.com/asjdf/goadb/internal/goadbserver/auth"
)

func TestEngineConnectSendsCNXNAndParsesDeviceBanner(t *testing.T) {
	host, device := net.Pipe()
	defer device.Close()

	errCh := make(chan error, 1)
	go func() {
		packet := readPacketForTest(t, device)
		if packet.Command != adbproto.CmdCNXN {
			errCh <- errUnexpectedPacket(packet, adbproto.CmdCNXN)
			return
		}
		if !strings.Contains(string(packet.Payload), "host::features=") {
			errCh <- errString("host CNXN banner did not include features")
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::ro.product.name=pixel;ro.product.model=Pixel_8;ro.product.device=shiba;features=shell_v2,cmd"),
		})
	}()

	engine := NewEngine(host, Options{Serial: "USB123", Type: TypeUSB})
	info, err := engine.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer engine.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd error: %v", err)
	}
	if info.Serial != "USB123" || info.State != StateDevice || info.Product != "pixel" || info.Model != "Pixel_8" || info.Device != "shiba" {
		t.Fatalf("Connect info = %#v", info)
	}
	if got, want := strings.Join(info.Features, ","), "shell_v2,cmd"; got != want {
		t.Fatalf("features = %q, want %q", got, want)
	}
}

func TestEngineConnectIgnoresStaleClosePackets(t *testing.T) {
	host, device := net.Pipe()
	defer device.Close()

	errCh := make(chan error, 1)
	go func() {
		if packet := readPacketForTest(t, device); packet.Command != adbproto.CmdCNXN {
			errCh <- errUnexpectedPacket(packet, adbproto.CmdCNXN)
			return
		}
		if err := adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdCLSE}); err != nil {
			errCh <- err
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::features=shell_v2"),
		})
	}()

	engine := NewEngine(host, Options{Serial: "USB123", Type: TypeUSB})
	info, err := engine.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer engine.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd error: %v", err)
	}
	if info.Serial != "USB123" || info.State != StateDevice {
		t.Fatalf("Connect info = %#v", info)
	}
}

func TestEngineConnectSignsAuthToken(t *testing.T) {
	host, device := net.Pipe()
	defer device.Close()
	keyPair := newTestKeyPair(t)

	errCh := make(chan error, 1)
	go func() {
		if packet := readPacketForTest(t, device); packet.Command != adbproto.CmdCNXN {
			errCh <- errUnexpectedPacket(packet, adbproto.CmdCNXN)
			return
		}
		token := []byte("12345678901234567890")
		if err := adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdAUTH, Arg0: adbproto.AuthToken, Payload: token}); err != nil {
			errCh <- err
			return
		}
		signature := readPacketForTest(t, device)
		if signature.Command != adbproto.CmdAUTH || signature.Arg0 != adbproto.AuthSignature || len(signature.Payload) == 0 {
			errCh <- errString("host did not send AUTH signature")
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::features=shell_v2"),
		})
	}()

	engine := NewEngine(host, Options{Serial: "USB123", Type: TypeUSB, KeyPair: keyPair})
	if _, err := engine.Connect(context.Background()); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer engine.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd error: %v", err)
	}
}

func TestEngineOpenReadsRemoteStreamAndAcknowledgesWRTE(t *testing.T) {
	host, device := connectedEnginePipe(t)
	engine := NewEngine(host, Options{Serial: "USB123", Type: TypeUSB})
	if _, err := engine.Connect(context.Background()); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer engine.Close()

	errCh := make(chan error, 1)
	go func() {
		open := readPacketForTest(t, device)
		if open.Command != adbproto.CmdOPEN || string(open.Payload) != "shell:id\x00" {
			errCh <- errString("unexpected OPEN packet")
			return
		}
		localID := open.Arg0
		remoteID := uint32(99)
		if err := adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdOKAY, Arg0: remoteID, Arg1: localID}); err != nil {
			errCh <- err
			return
		}
		if err := adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdWRTE, Arg0: remoteID, Arg1: localID, Payload: []byte("uid=0\n")}); err != nil {
			errCh <- err
			return
		}
		ack := readPacketForTest(t, device)
		if ack.Command != adbproto.CmdOKAY || ack.Arg0 != localID || ack.Arg1 != remoteID {
			errCh <- errString("host did not ACK WRTE with OKAY")
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdCLSE, Arg0: remoteID, Arg1: localID})
	}()

	stream, err := engine.Open(context.Background(), "shell:id")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "uid=0\n" {
		t.Fatalf("stream data = %q, want uid output", string(data))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd error: %v", err)
	}
}

func TestStreamWriteSendsWRTEAndWaitsForOKAY(t *testing.T) {
	host, device := connectedEnginePipe(t)
	engine := NewEngine(host, Options{Serial: "USB123", Type: TypeUSB})
	if _, err := engine.Connect(context.Background()); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer engine.Close()

	errCh := make(chan error, 1)
	go func() {
		open := readPacketForTest(t, device)
		localID := open.Arg0
		remoteID := uint32(99)
		if err := adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdOKAY, Arg0: remoteID, Arg1: localID}); err != nil {
			errCh <- err
			return
		}
		wrte := readPacketForTest(t, device)
		if wrte.Command != adbproto.CmdWRTE || wrte.Arg0 != localID || wrte.Arg1 != remoteID || string(wrte.Payload) != "input" {
			errCh <- errString("unexpected WRTE packet")
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{Command: adbproto.CmdOKAY, Arg0: remoteID, Arg1: localID})
	}()

	stream, err := engine.Open(context.Background(), "shell:")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if n, err := stream.Write([]byte("input")); err != nil || n != len("input") {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len("input"))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd error: %v", err)
	}
}

func connectedEnginePipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	host, device := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		packet := readPacketForTest(t, device)
		if packet.Command != adbproto.CmdCNXN {
			errCh <- errUnexpectedPacket(packet, adbproto.CmdCNXN)
			return
		}
		errCh <- adbproto.WritePacket(device, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::features=shell_v2,cmd"),
		})
	}()
	t.Cleanup(func() {
		host.Close()
		device.Close()
	})
	t.Cleanup(func() {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("fake connect setup returned error: %v", err)
			}
		default:
			t.Fatal("fake connect setup did not complete")
		}
	})
	return host, device
}

func readPacketForTest(t *testing.T, r io.Reader) adbproto.Packet {
	t.Helper()
	packet, err := adbproto.ReadPacket(r, adbproto.MaxPayload)
	if err != nil {
		t.Fatalf("ReadPacket returned error: %v", err)
	}
	return packet
}

func newTestKeyPair(t *testing.T) *auth.KeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	return &auth.KeyPair{PrivateKey: key, PublicKey: "pub"}
}

type errString string

func (e errString) Error() string { return string(e) }

func errUnexpectedPacket(packet adbproto.Packet, want uint32) error {
	return errString("unexpected packet command")
}
