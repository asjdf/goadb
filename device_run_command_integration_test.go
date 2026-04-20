package adb

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCommand_IntegrationStillWorksAfterInstall(t *testing.T) {
	device := integrationTestDevice(t)
	ensurePackageUninstalled(t, device)
	t.Cleanup(func() {
		ensurePackageUninstalled(t, device)
	})

	apkData := mustReadIntegrationTestAPK(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := device.InstallWithContext(ctx, bytes.NewReader(apkData), "-t"); err != nil {
		t.Fatalf("InstallWithContext() returned error:\n%s", ErrorWithCauseChain(err))
	}

	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantSub    string
		allowEmpty bool
	}{
		{
			name:    "echo",
			cmd:     "echo",
			args:    []string{"hello"},
			wantSub: "hello",
		},
		{
			name:       "getprop",
			cmd:        "getprop",
			args:       []string{"ro.product.model"},
			wantSub:    strings.TrimSpace(device.descriptor.serial),
			allowEmpty: true,
		},
		{
			name:    "pm list packages -u",
			cmd:     "pm",
			args:    []string{"list", "packages", "-u", deviceInstallTestPackageName},
			wantSub: "package:" + deviceInstallTestPackageName,
		},
		{
			name:    "pm path --user 0",
			cmd:     "pm",
			args:    []string{"path", "--user", "0", deviceInstallTestPackageName},
			wantSub: "package:/",
		},
	}

	for _, tt := range tests {
		output, err := device.RunCommand(tt.cmd, tt.args...)
		t.Logf("%s => err=%v output=%q", tt.name, err, output)
		if err != nil {
			t.Fatalf("%s error: %v", tt.name, err)
		}
		if tt.allowEmpty {
			continue
		}
		if !strings.Contains(output, tt.wantSub) {
			t.Fatalf("%s output = %q, want substring %q", tt.name, output, tt.wantSub)
		}
	}

	serial, err := device.Serial()
	t.Logf("serial after run-command probes => err=%v output=%q", err, serial)
	if err != nil {
		t.Fatalf("Serial() after run-command probes error: %v", err)
	}

	deviceInfo, err := device.DeviceInfo()
	t.Logf("device info after run-command probes => err=%v info=%+v", err, deviceInfo)
	if err != nil {
		t.Fatalf("DeviceInfo() after run-command probes error: %v", err)
	}
}
