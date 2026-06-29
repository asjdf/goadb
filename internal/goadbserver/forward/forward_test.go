package forward

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestManagerAddListRemoveForward(t *testing.T) {
	manager := New(nil)
	rule, err := manager.Add(context.Background(), "USB123", "tcp:0", "tcp:9000", false)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	defer manager.RemoveAll()

	if !strings.HasPrefix(rule.Local, "tcp:") || rule.Remote != "tcp:9000" || rule.Serial != "USB123" {
		t.Fatalf("rule = %#v", rule)
	}
	if got, want := manager.ListText(), "USB123 "+rule.Local+" tcp:9000\n"; got != want {
		t.Fatalf("ListText() = %q, want %q", got, want)
	}
	if removed := manager.Remove(rule.Local); !removed {
		t.Fatal("Remove returned false, want true")
	}
	if got := manager.ListText(); got != "" {
		t.Fatalf("ListText after remove = %q, want empty", got)
	}
}

func TestManagerAddNorebindFailsWhenLocalExists(t *testing.T) {
	manager := New(nil)
	rule, err := manager.Add(context.Background(), "USB123", "tcp:0", "tcp:9000", false)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	defer manager.RemoveAll()

	if _, err := manager.Add(context.Background(), "USB123", rule.Local, "tcp:9001", true); err == nil {
		t.Fatal("Add with norebind succeeded, want conflict error")
	}
}

func TestManagerBridgesAcceptedTCPConnectionToRemoteStream(t *testing.T) {
	remoteClient, remoteDevice := net.Pipe()
	defer remoteDevice.Close()

	opened := make(chan string, 1)
	manager := New(OpenerFunc(func(ctx context.Context, serial, service string) (io.ReadWriteCloser, error) {
		opened <- serial + " " + service
		return remoteClient, nil
	}))
	rule, err := manager.Add(context.Background(), "USB123", "tcp:0", "tcp:9000", false)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	defer manager.RemoveAll()

	localConn, err := net.DialTimeout("tcp", "127.0.0.1:"+strings.TrimPrefix(rule.Local, "tcp:"), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout returned error: %v", err)
	}
	defer localConn.Close()

	select {
	case got := <-opened:
		if got != "USB123 tcp:9000" {
			t.Fatalf("opener got %q, want USB123 tcp:9000", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for opener")
	}

	if _, err := localConn.Write([]byte("from-local\n")); err != nil {
		t.Fatalf("local write returned error: %v", err)
	}
	remoteReader := bufio.NewReader(remoteDevice)
	if got, err := remoteReader.ReadString('\n'); err != nil || got != "from-local\n" {
		t.Fatalf("remote read = %q, %v; want from-local", got, err)
	}

	if _, err := remoteDevice.Write([]byte("from-remote\n")); err != nil {
		t.Fatalf("remote write returned error: %v", err)
	}
	localReader := bufio.NewReader(localConn)
	if got, err := localReader.ReadString('\n'); err != nil || got != "from-remote\n" {
		t.Fatalf("local read = %q, %v; want from-remote", got, err)
	}
}
