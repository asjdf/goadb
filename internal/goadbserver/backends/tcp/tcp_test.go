package tcp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/asjdf/goadb/internal/goadbserver/adbproto"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

func TestBackendConnectDialsAndPerformsADBHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		packet, err := adbproto.ReadPacket(conn, adbproto.MaxPayload)
		if err != nil {
			errCh <- err
			return
		}
		if packet.Command != adbproto.CmdCNXN {
			t.Errorf("first packet command = %#x, want CNXN", packet.Command)
		}
		errCh <- adbproto.WritePacket(conn, adbproto.Packet{
			Command: adbproto.CmdCNXN,
			Arg0:    adbproto.Version,
			Arg1:    adbproto.MaxPayload,
			Payload: []byte("device::ro.product.name=pixel;features=shell_v2"),
		})
	}()

	backend := New(Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := backend.Connect(ctx, listener.Addr().String())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	defer backend.Close(info.Serial)

	if info.Serial != listener.Addr().String() || info.Type != transport.TypeTCP || info.State != transport.StateDevice || info.Product != "pixel" {
		t.Fatalf("Connect info = %#v", info)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("fake adbd returned error: %v", err)
	}
}
