package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goadb "github.com/asjdf/goadb"
	adberrors "github.com/asjdf/goadb/internal/errors"
)

func TestRunUsesPureGoBackendAndParsesServerFlags(t *testing.T) {
	factory := newFakeFactory()
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"-P", "5038", "devices"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if len(factory.configs) != 1 {
		t.Fatalf("created %d clients, want 1", len(factory.configs))
	}
	config := factory.configs[0]
	if config.Backend != goadb.ServerBackendGoadb {
		t.Fatalf("Backend = %v, want ServerBackendGoadb", config.Backend)
	}
	if config.PathToAdb != "" {
		t.Fatalf("PathToAdb = %q, want empty", config.PathToAdb)
	}
	if config.Host != "localhost" || config.Port != 5038 {
		t.Fatalf("Host/Port = %s/%d, want localhost/5038", config.Host, config.Port)
	}
}

func TestRunListenFlagWinsOverPortFlag(t *testing.T) {
	factory := newFakeFactory()
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"-P", "5038", "-L", "tcp:127.0.0.1:5040", "devices"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	config := factory.configs[0]
	if config.Host != "127.0.0.1" || config.Port != 5040 {
		t.Fatalf("Host/Port = %s/%d, want 127.0.0.1/5040", config.Host, config.Port)
	}
}

func TestRunShellCommandUsesSerialAndPrintsOutput(t *testing.T) {
	factory := newFakeFactory()
	factory.client.device.commandOutput = "ok\n"
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"-s", "ABC123", "shell", "echo", "ok"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if got := factory.client.lastDevice.String(); got != "DeviceSerial[ABC123]" {
		t.Fatalf("selected device = %s, want DeviceSerial[ABC123]", got)
	}
	if factory.client.device.command != "echo" || strings.Join(factory.client.device.commandArgs, " ") != "ok" {
		t.Fatalf("RunCommand = %q %q, want echo ok", factory.client.device.command, factory.client.device.commandArgs)
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("stdout = %q, want ok newline", stdout.String())
	}
}

func TestRunShellWithoutCommandStartsInteractiveShell(t *testing.T) {
	factory := newFakeFactory()
	shell := newDelayedOutputShell("remote prompt\n", time.Millisecond)
	factory.client.device.interactiveShell = shell
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"shell"}, strings.NewReader("exit\n"), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if factory.client.device.shellOptions.Command != "" {
		t.Fatalf("interactive command = %q, want device default shell", factory.client.device.shellOptions.Command)
	}
	if shell.input.String() != "exit\n" {
		t.Fatalf("shell input = %q, want exit newline", shell.input.String())
	}
	if stdout.String() != "remote prompt\n" {
		t.Fatalf("stdout = %q, want remote prompt newline", stdout.String())
	}
}

func TestRunShellWithoutCommandUsesPtyWhenStdinIsTerminal(t *testing.T) {
	factory := newFakeFactory()
	shell := newDelayedOutputShell("remote prompt\n", time.Millisecond)
	factory.client.device.interactiveShell = shell
	var stdout, stderr bytes.Buffer
	withInputTerminal(t, true)

	exitCode := runWithClientFactory([]string{"shell"}, strings.NewReader("exit\r\n"), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if !factory.client.device.shellOptions.Pty {
		t.Fatal("shellOptions.Pty = false, want true for terminal stdin")
	}
	if shell.input.String() != "exit\r\n" {
		t.Fatalf("shell input = %q, want terminal input forwarded without line-ending normalization", shell.input.String())
	}
}

func TestRunShellWithoutCommandSendsCloseStdinWhenInputEnds(t *testing.T) {
	factory := newFakeFactory()
	shell := newCloseStdinOutputShell("remote done\n")
	factory.client.device.interactiveShell = shell
	var stdout, stderr bytes.Buffer

	done := make(chan int, 1)
	go func() {
		done <- runWithClientFactory([]string{"shell"}, strings.NewReader("id\n"), &stdout, &stderr, factory.newClient)
	}()

	select {
	case exitCode := <-done:
		if exitCode != 0 {
			t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
		}
	case <-time.After(500 * time.Millisecond):
		_ = shell.Close()
		t.Fatal("interactive shell did not finish; want stdin EOF to send close-stdin")
	}
	if !shell.closeStdinCalled {
		t.Fatal("CloseStdin was not called")
	}
	if stdout.String() != "remote done\n" {
		t.Fatalf("stdout = %q, want remote done newline", stdout.String())
	}
}

func TestRunShellWithoutCommandWaitsForInteractiveOutputAfterInputEnds(t *testing.T) {
	factory := newFakeFactory()
	shell := newDelayedOutputShell("GOADB_INTERACTIVE_OK\n", 50*time.Millisecond)
	factory.client.device.interactiveShell = shell
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"shell"}, strings.NewReader("echo GOADB_INTERACTIVE_OK\nexit\n"), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if stdout.String() != "GOADB_INTERACTIVE_OK\n" {
		t.Fatalf("stdout = %q, want delayed interactive output", stdout.String())
	}
}

func TestRunShellWithoutCommandNormalizesWindowsLineEndings(t *testing.T) {
	factory := newFakeFactory()
	shell := newDelayedOutputShell("done\n", time.Millisecond)
	factory.client.device.interactiveShell = shell
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"shell"}, strings.NewReader("id\r\nls\rexit\n"), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if shell.input.String() != "id\nls\nexit\n" {
		t.Fatalf("shell input = %q, want CRLF/CR normalized to LF", shell.input.String())
	}
}

func TestRunInstallUsesLastArgAsAPKAndPassesInstallArgs(t *testing.T) {
	factory := newFakeFactory()
	apkPath := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(apkPath, []byte("apk bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"install", "-r", "-t", apkPath}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if strings.Join(factory.client.device.installArgs, " ") != "-r -t" {
		t.Fatalf("install args = %q, want -r -t", factory.client.device.installArgs)
	}
	if string(factory.client.device.installedAPK) != "apk bytes" {
		t.Fatalf("installed APK = %q, want apk bytes", factory.client.device.installedAPK)
	}
	if stdout.String() != "Success\n" {
		t.Fatalf("stdout = %q, want Success newline", stdout.String())
	}
}

func TestRunPushAppendsLocalFilenameWhenRemotePathIsDirectory(t *testing.T) {
	factory := newFakeFactory()
	factory.client.device.statEntries = map[string]*goadb.DirEntry{
		"/data/local/tmp": {Mode: os.ModeDir | 0o755},
	}
	localPath := filepath.Join(t.TempDir(), "goadb_cli.exe")
	if err := os.WriteFile(localPath, []byte("exe"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"push", localPath, "/data/local/tmp"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
	}
	if got, want := factory.client.device.openWritePath, "/data/local/tmp/goadb_cli.exe"; got != want {
		t.Fatalf("OpenWrite path = %q, want %q", got, want)
	}
}

func TestRunPushReportsRemoteCloseError(t *testing.T) {
	factory := newFakeFactory()
	factory.client.device.openWriteCloser = &errorOnCloseWriter{closeErr: errors.New("remote sync failed")}
	localPath := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(localPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"push", localPath, "/data/local/tmp/payload.bin"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode == 0 {
		t.Fatal("exitCode = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "remote sync failed") {
		t.Fatalf("stderr = %q, want remote close error", stderr.String())
	}
}

func TestRunForwardCommands(t *testing.T) {
	t.Run("add tcp forward", func(t *testing.T) {
		factory := newFakeFactory()
		var stdout, stderr bytes.Buffer

		exitCode := runWithClientFactory([]string{"forward", "tcp:7000", "tcp:8000"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
		}
		device := factory.client.device
		if device.forwardLocalKind != goadb.ForwardProtocolKindTCP || device.forwardLocalPort != 7000 {
			t.Fatalf("local forward = %s:%d, want tcp:7000", device.forwardLocalKind, device.forwardLocalPort)
		}
		if device.forwardRemoteKind != goadb.ForwardProtocolKindTCP || device.forwardRemotePort != 8000 {
			t.Fatalf("remote forward = %s:%d, want tcp:8000", device.forwardRemoteKind, device.forwardRemotePort)
		}
	})

	t.Run("list forwards", func(t *testing.T) {
		factory := newFakeFactory()
		factory.client.forwards = []goadb.Forward{{
			Serial:             "ABC123",
			LocalProtocolKind:  goadb.ForwardProtocolKindTCP,
			LocalPort:          7000,
			RemoteProtocolKind: goadb.ForwardProtocolKindTCP,
			RemotePort:         8000,
		}}
		var stdout, stderr bytes.Buffer

		exitCode := runWithClientFactory([]string{"forward", "--list"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
		}
		if stdout.String() != "ABC123 tcp:7000 tcp:8000\n" {
			t.Fatalf("stdout = %q, want forward list", stdout.String())
		}
	})

	t.Run("remove tcp forward", func(t *testing.T) {
		factory := newFakeFactory()
		var stdout, stderr bytes.Buffer

		exitCode := runWithClientFactory([]string{"forward", "--remove", "tcp:7000"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
		}
		device := factory.client.device
		if device.removeForwardKind != goadb.ForwardProtocolKindTCP || device.removeForwardPort != 7000 {
			t.Fatalf("remove forward = %s:%d, want tcp:7000", device.removeForwardKind, device.removeForwardPort)
		}
	})

	t.Run("remove all forwards", func(t *testing.T) {
		factory := newFakeFactory()
		var stdout, stderr bytes.Buffer

		exitCode := runWithClientFactory([]string{"forward", "--remove-all"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, stderr = %q", exitCode, stderr.String())
		}
		if !factory.client.device.removeAllForwards {
			t.Fatal("RemoveAllPortForwards was not called")
		}
	})
}

func TestRunRejectsInvalidForwardEndpoint(t *testing.T) {
	factory := newFakeFactory()
	var stdout, stderr bytes.Buffer

	exitCode := runWithClientFactory([]string{"forward", "localabstract:foo", "tcp:8000"}, strings.NewReader(""), &stdout, &stderr, factory.newClient)

	if exitCode == 0 {
		t.Fatal("exitCode = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "only tcp forward endpoints are supported") {
		t.Fatalf("stderr = %q, want tcp-only error", stderr.String())
	}
}

type fakeFactory struct {
	client  *fakeClient
	configs []goadb.ServerConfig
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{client: &fakeClient{device: &fakeDevice{}}}
}

func (f *fakeFactory) newClient(config goadb.ServerConfig) (cliClient, error) {
	f.configs = append(f.configs, config)
	return f.client, nil
}

type fakeClient struct {
	device     *fakeDevice
	lastDevice goadb.DeviceDescriptor
	forwards   []goadb.Forward
}

func (c *fakeClient) ListDevices() ([]*goadb.DeviceInfo, error) {
	return []*goadb.DeviceInfo{{Serial: "ABC123"}}, nil
}

func (c *fakeClient) Device(descriptor goadb.DeviceDescriptor) cliDevice {
	c.lastDevice = descriptor
	return c.device
}

func (c *fakeClient) ForwardList() ([]goadb.Forward, error) {
	return c.forwards, nil
}

type fakeDevice struct {
	command       string
	commandArgs   []string
	commandOutput string

	shell            *fakeShell
	shellOptions     goadb.ShellOptions
	interactiveShell cliShell

	statEntries     map[string]*goadb.DirEntry
	openWritePath   string
	openWriteCloser io.WriteCloser

	installArgs  []string
	installedAPK []byte

	forwardLocalKind  goadb.ForwardProtocolKind
	forwardLocalPort  int
	forwardRemoteKind goadb.ForwardProtocolKind
	forwardRemotePort int

	removeForwardKind goadb.ForwardProtocolKind
	removeForwardPort int
	removeAllForwards bool
}

func (d *fakeDevice) RunCommand(cmd string, args ...string) (string, error) {
	d.command = cmd
	d.commandArgs = append([]string(nil), args...)
	return d.commandOutput, nil
}

func (d *fakeDevice) OpenShell(options goadb.ShellOptions) (cliShell, error) {
	d.shellOptions = options
	if d.interactiveShell != nil {
		return d.interactiveShell, nil
	}
	if d.shell == nil {
		d.shell = newFakeShell("")
	}
	return d.shell, nil
}

func (d *fakeDevice) Stat(path string) (*goadb.DirEntry, error) {
	if d.statEntries != nil {
		if entry, ok := d.statEntries[path]; ok {
			return entry, nil
		}
		return nil, adberrors.Errorf(adberrors.FileNoExistError, "file doesn't exist")
	}
	return &goadb.DirEntry{Size: 0}, nil
}

func (d *fakeDevice) OpenRead(path string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (d *fakeDevice) OpenWrite(path string, perms os.FileMode, mtime time.Time) (io.WriteCloser, error) {
	d.openWritePath = path
	if d.openWriteCloser != nil {
		return d.openWriteCloser, nil
	}
	return nopWriteCloser{Writer: io.Discard}, nil
}

func (d *fakeDevice) Install(apk goadb.LenReader, args ...string) error {
	d.installArgs = append([]string(nil), args...)
	data, err := io.ReadAll(apk)
	if err != nil {
		return err
	}
	d.installedAPK = data
	return nil
}

func (d *fakeDevice) StartPortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int, remoteProtocolKind goadb.ForwardProtocolKind, remotePort int) error {
	d.forwardLocalKind = localProtocolKind
	d.forwardLocalPort = localPort
	d.forwardRemoteKind = remoteProtocolKind
	d.forwardRemotePort = remotePort
	return nil
}

func (d *fakeDevice) RemovePortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int) error {
	d.removeForwardKind = localProtocolKind
	d.removeForwardPort = localPort
	return nil
}

func (d *fakeDevice) RemoveAllPortForwards() error {
	d.removeAllForwards = true
	return nil
}

type fakeShell struct {
	input  bytes.Buffer
	output *bytes.Reader
}

func newFakeShell(output string) *fakeShell {
	return &fakeShell{output: bytes.NewReader([]byte(output))}
}

func (s *fakeShell) Read(p []byte) (int, error) {
	return s.output.Read(p)
}

func (s *fakeShell) Write(p []byte) (int, error) {
	return s.input.Write(p)
}

func (s *fakeShell) Close() error {
	return nil
}

func (s *fakeShell) CloseStdin() error {
	return nil
}

func (s *fakeShell) SetWindowSize(rows, cols, xPixels, yPixels int) error {
	return nil
}

type delayedOutputShell struct {
	input       bytes.Buffer
	output      []byte
	delay       time.Duration
	ready       chan struct{}
	closed      chan struct{}
	readyOnce   sync.Once
	closeOnce   sync.Once
	outputOnce  bool
	outputMutex sync.Mutex
}

func newDelayedOutputShell(output string, delay time.Duration) *delayedOutputShell {
	return &delayedOutputShell{
		output: []byte(output),
		delay:  delay,
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (s *delayedOutputShell) Read(p []byte) (int, error) {
	select {
	case <-s.ready:
	case <-s.closed:
		return 0, io.EOF
	}

	s.outputMutex.Lock()
	defer s.outputMutex.Unlock()
	if s.outputOnce {
		return 0, io.EOF
	}
	s.outputOnce = true
	return copy(p, s.output), io.EOF
}

func (s *delayedOutputShell) Write(p []byte) (int, error) {
	n, err := s.input.Write(p)
	s.readyOnce.Do(func() {
		time.AfterFunc(s.delay, func() {
			close(s.ready)
		})
	})
	return n, err
}

func (s *delayedOutputShell) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *delayedOutputShell) CloseStdin() error {
	return nil
}

func (s *delayedOutputShell) SetWindowSize(rows, cols, xPixels, yPixels int) error {
	return nil
}

type closeStdinOutputShell struct {
	input            bytes.Buffer
	output           []byte
	closeStdin       chan struct{}
	closed           chan struct{}
	closeStdinOnce   sync.Once
	closeOnce        sync.Once
	closeStdinCalled bool
}

func newCloseStdinOutputShell(output string) *closeStdinOutputShell {
	return &closeStdinOutputShell{
		output:     []byte(output),
		closeStdin: make(chan struct{}),
		closed:     make(chan struct{}),
	}
}

func (s *closeStdinOutputShell) Read(p []byte) (int, error) {
	select {
	case <-s.closeStdin:
	case <-s.closed:
		return 0, io.EOF
	}
	if len(s.output) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.output)
	s.output = s.output[n:]
	if len(s.output) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func (s *closeStdinOutputShell) Write(p []byte) (int, error) {
	return s.input.Write(p)
}

func (s *closeStdinOutputShell) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *closeStdinOutputShell) CloseStdin() error {
	s.closeStdinCalled = true
	s.closeStdinOnce.Do(func() {
		close(s.closeStdin)
	})
	return nil
}

func (s *closeStdinOutputShell) SetWindowSize(rows, cols, xPixels, yPixels int) error {
	return nil
}

func withInputTerminal(t *testing.T, terminal bool) {
	t.Helper()
	original := inputIsTerminal
	inputIsTerminal = func(io.Reader) bool {
		return terminal
	}
	t.Cleanup(func() {
		inputIsTerminal = original
	})
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error {
	return nil
}

type errorOnCloseWriter struct {
	bytes.Buffer
	closeErr error
}

func (w *errorOnCloseWriter) Close() error {
	return w.closeErr
}
