package adb

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestInProcessGoadbServerLifecycle(t *testing.T) {
	port := freeTestTCPPort(t)
	keyPath := writeTestADBKeyPair(t)
	client, err := NewWithConfig(ServerConfig{
		Backend: ServerBackendGoadb,
		Host:    "localhost",
		Port:    port,
		KeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}

	if err := client.StartServer(false); err != nil {
		t.Fatalf("StartServer(false) returned error: %v", err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = client.KillServer()
		}
	})

	version, err := client.ServerVersion()
	if err != nil {
		t.Fatalf("ServerVersion returned error: %v", err)
	}
	if version != 41 {
		t.Fatalf("ServerVersion = %d, want 41", version)
	}

	if err := client.KillServer(); err != nil {
		t.Fatalf("KillServer returned error: %v", err)
	}
	killed = true
	assertTestPortClosed(t, port, 5*time.Second)
}

func writeTestADBKeyPair(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "adbkey")
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("WriteFile private key returned error: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("test-public-key"), 0o600); err != nil {
		t.Fatalf("WriteFile public key returned error: %v", err)
	}
	return keyPath
}

func freeTestTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func assertTestPortClosed(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	address := net.JoinHostPort("localhost", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server port %d stayed open after kill for %s", port, timeout)
}
