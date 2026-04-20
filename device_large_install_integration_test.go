package adb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	deviceInstallTestAPKPathEnv     = "GOADB_TEST_INSTALL_APK"
	deviceInstallTestAPKPackageEnv  = "GOADB_TEST_INSTALL_PKG"
	deviceInstallTestStartServerEnv = "GOADB_TEST_START_SERVER"
	deviceInstallTestKeyPathEnv     = "GOADB_TEST_KEY_PATH"
	deviceInstallTestPathToAdbEnv   = "GOADB_TEST_PATH_TO_ADB"
)

func TestInstallWithContext_IntegrationLargeAPK(t *testing.T) {
	device := integrationTestDeviceWithOptions(t, strings.EqualFold(strings.TrimSpace(os.Getenv(deviceInstallTestStartServerEnv)), "1"))

	apkPath := strings.TrimSpace(os.Getenv(deviceInstallTestAPKPathEnv))
	if apkPath == "" {
		t.Skipf("set %s to run large-apk install test", deviceInstallTestAPKPathEnv)
	}
	pkgName := strings.TrimSpace(os.Getenv(deviceInstallTestAPKPackageEnv))
	if pkgName == "" {
		t.Skipf("set %s to validate large-apk install state", deviceInstallTestAPKPackageEnv)
	}

	ensureNamedPackageUninstalled(t, device, pkgName)

	apkData, err := os.ReadFile(apkPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", apkPath, err)
	}

	for _, cmd := range []string{
		"settings put global verifier_verify_adb_installs 0",
		"settings put global package_verifier_enable 0",
	} {
		output, err := device.RunCommand(cmd)
		t.Logf("before install cmd %q => err=%v output=%q", cmd, err, output)
		if err != nil {
			t.Fatalf("RunCommand(%q) error: %v", cmd, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err = device.InstallWithContext(ctx, bytes.NewReader(apkData), "-g")
	if err != nil {
		t.Fatalf("InstallWithContext() returned error:\n%s", ErrorWithCauseChain(err))
	}

	waitForNamedPackageInstalledState(t, device, pkgName, true, 20*time.Second)
}

func integrationTestDeviceWithOptions(t *testing.T, startServer bool) *Device {
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

	serverConfig := ServerConfig{
		PathToAdb: strings.TrimSpace(os.Getenv(deviceInstallTestPathToAdbEnv)),
		KeyPath:   strings.TrimSpace(os.Getenv(deviceInstallTestKeyPathEnv)),
	}

	client, err := NewWithConfig(serverConfig)
	if err != nil {
		t.Fatalf("NewWithConfig() error: %v", err)
	}
	if startServer {
		if err := client.StartServer(false); err != nil {
			t.Fatalf("StartServer(false) error: %v", err)
		}
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

func ensureNamedPackageUninstalled(t *testing.T, device *Device, pkgName string) {
	t.Helper()

	installed, err := isNamedPackageInstalled(device, pkgName)
	if err != nil {
		t.Fatalf("isNamedPackageInstalled(%s) error: %v", pkgName, err)
	}
	if !installed {
		return
	}

	output, err := device.RunCommand("pm", "uninstall", pkgName)
	if err != nil {
		t.Fatalf("pm uninstall %s error: %v", pkgName, err)
	}
	if !strings.Contains(output, "Success") {
		t.Fatalf("pm uninstall %s output = %q, want Success", pkgName, output)
	}

	waitForNamedPackageInstalledState(t, device, pkgName, false, 20*time.Second)
}

func waitForNamedPackageInstalledState(t *testing.T, device *Device, pkgName string, wantInstalled bool, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var (
		lastInstalled bool
		lastErr       error
	)
	for time.Now().Before(deadline) {
		lastInstalled, lastErr = isNamedPackageInstalled(device, pkgName)
		if lastErr == nil && lastInstalled == wantInstalled {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	if lastErr != nil {
		t.Fatalf("timed out waiting for package %s installed=%v: %v", pkgName, wantInstalled, lastErr)
	}
	t.Fatalf("timed out waiting for package %s installed=%v, last installed=%v", pkgName, wantInstalled, lastInstalled)
}

func isNamedPackageInstalled(device *Device, pkgName string) (bool, error) {
	output, err := device.RunCommand("pm", "list", "packages", "-u", pkgName)
	if err != nil {
		return false, err
	}
	return strings.Contains(output, "package:"+pkgName), nil
}
