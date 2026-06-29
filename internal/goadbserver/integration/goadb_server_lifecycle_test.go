//go:build integration

package integration

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	adb "github.com/asjdf/goadb"
)

func TestGoadbStartAndKillServerLifecycle(t *testing.T) {
	serverPath := os.Getenv("GOADB_SERVER_BIN")
	if serverPath == "" {
		t.Skip("set GOADB_SERVER_BIN to the built goadb-server.exe")
	}

	port := freeTCPPort(t)
	client, err := adb.NewWithConfig(adb.ServerConfig{
		PathToAdb: serverPath,
		Host:      "localhost",
		Port:      port,
		KeyPath:   goadbKeyDir(t),
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}

	if err := client.StartServer(false); err != nil {
		t.Fatalf("StartServer(false) returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = client.KillServer()
	})

	if _, err := client.ServerVersion(); err != nil {
		t.Fatalf("ServerVersion after StartServer returned error: %v", err)
	}
	if err := client.KillServer(); err != nil {
		t.Fatalf("KillServer returned error: %v", err)
	}
	assertPortClosed(t, port, 5*time.Second)
}

func TestGoadbStartServerWithDefaultKeyAndEmptyChildEnvironment(t *testing.T) {
	serverPath := os.Getenv("GOADB_SERVER_BIN")
	if serverPath == "" {
		t.Skip("set GOADB_SERVER_BIN to the built goadb-server.exe")
	}

	port := freeTCPPort(t)
	client, err := adb.NewWithConfig(adb.ServerConfig{
		PathToAdb: serverPath,
		Host:      "localhost",
		Port:      port,
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	if err := client.StartServer(false); err != nil {
		t.Fatalf("StartServer(false) returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = client.KillServer()
	})
	if _, err := client.ServerVersion(); err != nil {
		t.Fatalf("ServerVersion returned error: %v", err)
	}
	if err := client.KillServer(); err != nil {
		t.Fatalf("KillServer returned error: %v", err)
	}
	assertPortClosed(t, port, 5*time.Second)
}

func goadbKeyDir(t *testing.T) string {
	t.Helper()
	source := getenvDefault("GOADB_ADBKEY", `C:\Users\Any\.android\adbkey`)
	data, err := os.ReadFile(filepath.Clean(source))
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", source, err)
	}
	pubData, err := os.ReadFile(filepath.Clean(source + ".pub"))
	if err != nil {
		t.Fatalf("ReadFile(%s.pub) returned error: %v", source, err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "goadb.adb_key")
	if err := os.WriteFile(keyPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile key returned error: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", pubData, 0o600); err != nil {
		t.Fatalf("WriteFile public key returned error: %v", err)
	}
	return dir
}

func assertPortClosed(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server port %d stayed open after kill for %s", port, timeout)
}
