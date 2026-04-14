package adb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	deviceInstallTestDeviceIPEnv   = "GOADB_TEST_DEVICE_IP"
	deviceInstallTestDevicePortEnv = "GOADB_TEST_DEVICE_PORT"
	deviceInstallTestPackageName   = "com.omarea.vaddin"
	deviceInstallTestDefaultPort   = 5555
)

func TestInstallWithContext_IntegrationInstallsAPK(t *testing.T) {
	device := integrationTestDevice(t)
	ensurePackageUninstalled(t, device)
	t.Cleanup(func() {
		ensurePackageUninstalled(t, device)
	})

	apkData := mustReadIntegrationTestAPK(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := device.InstallWithContext(ctx, bytes.NewReader(apkData), "-t")
	if err != nil {
		t.Fatalf("InstallWithContext() returned error:\n%s", ErrorWithCauseChain(err))
	}

	waitForPackageInstalledState(t, device, true, 10*time.Second)
}

func TestInstallWithContext_IntegrationCancelsInstall(t *testing.T) {
	device := integrationTestDevice(t)
	ensurePackageUninstalled(t, device)
	t.Cleanup(func() {
		ensurePackageUninstalled(t, device)
	})

	apkData := mustReadIntegrationTestAPK(t)
	apk := newSlowLenReader(apkData, 1, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- device.InstallWithContext(ctx, apk, "-t")
	}()

	<-apk.started()
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-errCh
	if err == nil {
		t.Fatal("InstallWithContext() succeeded, want cancellation error")
	}
	if !HasErrCode(err, NetworkError) {
		t.Fatalf("InstallWithContext() error =\n%s\nwant NetworkError", ErrorWithCauseChain(err))
	}
	if !strings.Contains(ErrorWithCauseChain(err), "install canceled") {
		t.Fatalf("InstallWithContext() error =\n%s\nwant cancellation error", ErrorWithCauseChain(err))
	}

	waitForPackageInstalledState(t, device, false, 10*time.Second)
}

func integrationTestDevice(t *testing.T) *Device {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping real-device install tests in short mode")
	}

	deviceIP := strings.TrimSpace(os.Getenv(deviceInstallTestDeviceIPEnv))
	if deviceIP == "" {
		t.Skipf("set %s to run real-device install tests", deviceInstallTestDeviceIPEnv)
	}

	port := deviceInstallTestDefaultPort
	if rawPort := strings.TrimSpace(os.Getenv(deviceInstallTestDevicePortEnv)); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil {
			t.Fatalf("invalid %s=%q: %v", deviceInstallTestDevicePortEnv, rawPort, err)
		}
		port = parsedPort
	}

	client, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := client.Connect(deviceIP, port); err != nil {
		t.Fatalf("Connect(%s, %d) error: %v", deviceIP, port, err)
	}

	serial := fmt.Sprintf("%s:%d", deviceIP, port)
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		device, err := client.DeviceBySerial(serial, true)
		if err == nil {
			return device
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}

	t.Fatalf("device %s did not become online: %v", serial, lastErr)
	return nil
}

func ensurePackageUninstalled(t *testing.T, device *Device) {
	t.Helper()

	installed, err := isPackageInstalled(device)
	if err != nil {
		t.Fatalf("isPackageInstalled() error: %v", err)
	}
	if !installed {
		return
	}

	output, err := device.RunCommand("pm", "uninstall", deviceInstallTestPackageName)
	if err != nil {
		t.Fatalf("pm uninstall error: %v", err)
	}
	if !strings.Contains(output, "Success") {
		t.Fatalf("pm uninstall output = %q, want Success", output)
	}

	waitForPackageInstalledState(t, device, false, 10*time.Second)
}

func waitForPackageInstalledState(t *testing.T, device *Device, wantInstalled bool, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var (
		lastInstalled bool
		lastErr       error
	)
	for time.Now().Before(deadline) {
		lastInstalled, lastErr = isPackageInstalled(device)
		if lastErr == nil && lastInstalled == wantInstalled {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	if lastErr != nil {
		t.Fatalf("timed out waiting for package installed=%v: %v", wantInstalled, lastErr)
	}
	t.Fatalf("timed out waiting for package installed=%v, last installed=%v", wantInstalled, lastInstalled)
}

func isPackageInstalled(device *Device) (bool, error) {
	output, err := device.RunCommand("pm", "list", "packages", deviceInstallTestPackageName)
	if err != nil {
		return false, err
	}
	return strings.Contains(output, "package:"+deviceInstallTestPackageName), nil
}

func mustReadIntegrationTestAPK(t *testing.T) []byte {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	apkPath := filepath.Join(filepath.Dir(thisFile), "testdata", "test.apk")
	apkData, err := os.ReadFile(apkPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", apkPath, err)
	}
	return apkData
}

type slowLenReader struct {
	data      []byte
	chunkSize int
	delay     time.Duration

	offset      int
	startedOnce sync.Once
	startedCh   chan struct{}
}

func newSlowLenReader(data []byte, chunkSize int, delay time.Duration) *slowLenReader {
	return &slowLenReader{
		data:      data,
		chunkSize: chunkSize,
		delay:     delay,
		startedCh: make(chan struct{}),
	}
}

func (r *slowLenReader) Len() int {
	return len(r.data)
}

func (r *slowLenReader) Read(p []byte) (int, error) {
	r.startedOnce.Do(func() {
		close(r.startedCh)
	})

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
