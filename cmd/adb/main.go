package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	goadb "github.com/asjdf/goadb"
	"github.com/cheggaaa/pb"
	"golang.org/x/term"
)

const StdIoFilename = "-"

type cliClient interface {
	ListDevices() ([]*goadb.DeviceInfo, error)
	Device(goadb.DeviceDescriptor) cliDevice
	ForwardList() ([]goadb.Forward, error)
}

type cliShell interface {
	io.ReadWriteCloser
	CloseStdin() error
	SetWindowSize(rows, cols, xPixels, yPixels int) error
}

type cliDevice interface {
	RunCommand(cmd string, args ...string) (string, error)
	OpenShell(options goadb.ShellOptions) (cliShell, error)
	Stat(path string) (*goadb.DirEntry, error)
	OpenRead(path string) (io.ReadCloser, error)
	OpenWrite(path string, perms os.FileMode, mtime time.Time) (io.WriteCloser, error)
	Install(apk goadb.LenReader, args ...string) error
	StartPortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int, remoteProtocolKind goadb.ForwardProtocolKind, remotePort int) error
	RemovePortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int) error
	RemoveAllPortForwards() error
}

type clientFactory func(config goadb.ServerConfig) (cliClient, error)

type realClient struct {
	adb *goadb.Adb
}

func newRealClient(config goadb.ServerConfig) (cliClient, error) {
	client, err := goadb.NewWithConfig(config)
	if err != nil {
		return nil, err
	}
	return realClient{adb: client}, nil
}

func (c realClient) ListDevices() ([]*goadb.DeviceInfo, error) {
	return c.adb.ListDevices()
}

func (c realClient) Device(descriptor goadb.DeviceDescriptor) cliDevice {
	return realDevice{device: c.adb.Device(descriptor)}
}

func (c realClient) ForwardList() ([]goadb.Forward, error) {
	return c.adb.ForwardList()
}

type realDevice struct {
	device *goadb.Device
}

func (d realDevice) RunCommand(cmd string, args ...string) (string, error) {
	return d.device.RunCommand(cmd, args...)
}

func (d realDevice) OpenShell(options goadb.ShellOptions) (cliShell, error) {
	return d.device.OpenShell(options)
}

func (d realDevice) Stat(path string) (*goadb.DirEntry, error) {
	return d.device.Stat(path)
}

func (d realDevice) OpenRead(path string) (io.ReadCloser, error) {
	return d.device.OpenRead(path)
}

func (d realDevice) OpenWrite(path string, perms os.FileMode, mtime time.Time) (io.WriteCloser, error) {
	return d.device.OpenWrite(path, perms, mtime)
}

func (d realDevice) Install(apk goadb.LenReader, args ...string) error {
	return d.device.Install(apk, args...)
}

func (d realDevice) StartPortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int, remoteProtocolKind goadb.ForwardProtocolKind, remotePort int) error {
	return d.device.StartPortForwarding(localProtocolKind, localPort, remoteProtocolKind, remotePort)
}

func (d realDevice) RemovePortForwarding(localProtocolKind goadb.ForwardProtocolKind, localPort int) error {
	return d.device.RemovePortForwarding(localProtocolKind, localPort)
}

func (d realDevice) RemoveAllPortForwards() error {
	return d.device.RemoveAllPortForwards()
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithClientFactory(args, stdin, stdout, stderr, newRealClient)
}

func runWithClientFactory(args []string, stdin io.Reader, stdout, stderr io.Writer, factory clientFactory) int {
	opts, command, commandArgs, err := parseGlobalArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		printUsage(stderr)
		return 1
	}
	if command == "" {
		printUsage(stderr)
		return 1
	}

	config, err := buildServerConfig(opts)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	client, err := factory(config)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	device := parseDevice(opts)
	switch command {
	case "devices":
		long, err := parseDevicesArgs(commandArgs)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return listDevices(client, long, stdout, stderr)
	case "shell":
		return runShell(client, commandArgs, device, stdin, stdout, stderr)
	case "pull":
		showProgress, positional := parseProgressArgs(commandArgs)
		return pull(client, showProgress, positional, device, stdout, stderr)
	case "push":
		showProgress, positional := parseProgressArgs(commandArgs)
		return push(client, showProgress, positional, device, stdin, stderr)
	case "install":
		return install(client, commandArgs, device, stdin, stdout, stderr)
	case "forward":
		return forward(client, commandArgs, device, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "error: unknown command:", command)
		printUsage(stderr)
		return 1
	}
}

type globalOptions struct {
	serial  string
	listen  string
	port    int
	hasPort bool
}

func parseGlobalArgs(args []string) (globalOptions, string, []string, error) {
	var opts globalOptions

	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case arg == "-s" || arg == "--serial":
			value, next, err := requireFlagValue(args, i, arg)
			if err != nil {
				return opts, "", nil, err
			}
			opts.serial = value
			i = next
		case strings.HasPrefix(arg, "--serial="):
			opts.serial = strings.TrimPrefix(arg, "--serial=")
			i++
		case strings.HasPrefix(arg, "-s") && len(arg) > 2:
			opts.serial = arg[2:]
			i++
		case arg == "-L":
			value, next, err := requireFlagValue(args, i, arg)
			if err != nil {
				return opts, "", nil, err
			}
			opts.listen = value
			i = next
		case strings.HasPrefix(arg, "-L") && len(arg) > 2:
			opts.listen = arg[2:]
			i++
		case arg == "-P":
			value, next, err := requireFlagValue(args, i, arg)
			if err != nil {
				return opts, "", nil, err
			}
			port, err := parseServerPort(value)
			if err != nil {
				return opts, "", nil, err
			}
			opts.port = port
			opts.hasPort = true
			i = next
		case strings.HasPrefix(arg, "-P") && len(arg) > 2:
			port, err := parseServerPort(arg[2:])
			if err != nil {
				return opts, "", nil, err
			}
			opts.port = port
			opts.hasPort = true
			i++
		case strings.HasPrefix(arg, "-"):
			return opts, "", nil, fmt.Errorf("unknown global flag %s", arg)
		default:
			return opts, arg, args[i+1:], nil
		}
	}

	return opts, "", nil, nil
}

func requireFlagValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index + 1, fmt.Errorf("%s requires a value", flag)
	}
	return args[index+1], index + 2, nil
}

func buildServerConfig(opts globalOptions) (goadb.ServerConfig, error) {
	config := goadb.ServerConfig{
		Backend: goadb.ServerBackendGoadb,
	}
	if opts.hasPort {
		config.Host = "localhost"
		config.Port = opts.port
	}
	if opts.listen != "" {
		host, port, err := parseListenSpec(opts.listen)
		if err != nil {
			return goadb.ServerConfig{}, err
		}
		config.Host = host
		config.Port = port
	}
	return config, nil
}

func parseListenSpec(spec string) (string, int, error) {
	if !strings.HasPrefix(spec, "tcp:") {
		return "", 0, fmt.Errorf("unsupported listen spec %q", spec)
	}

	address := strings.TrimPrefix(spec, "tcp:")
	if address == "" {
		return "", 0, fmt.Errorf("missing tcp listen address")
	}
	if !strings.Contains(address, ":") {
		port, err := parseServerPort(address)
		return "localhost", port, err
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid tcp listen spec %q: %w", spec, err)
	}
	if host == "" {
		host = "localhost"
	}
	port, err := parseServerPort(portText)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func parseServerPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid adb server port %q", value)
	}
	return port, nil
}

func parseDevice(opts globalOptions) goadb.DeviceDescriptor {
	if opts.serial != "" {
		return goadb.DeviceWithSerial(opts.serial)
	}
	return goadb.AnyDevice()
}

func parseDevicesArgs(args []string) (bool, error) {
	long := false
	for _, arg := range args {
		switch arg {
		case "-l", "--long":
			long = true
		default:
			return false, fmt.Errorf("unknown devices argument %s", arg)
		}
	}
	return long, nil
}

func parseProgressArgs(args []string) (bool, []string) {
	showProgress := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "-p", "--progress":
			showProgress = true
		default:
			positional = append(positional, arg)
		}
	}
	return showProgress, positional
}

func listDevices(client cliClient, long bool, stdout, stderr io.Writer) int {
	devices, err := client.ListDevices()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	for _, device := range devices {
		if long {
			if device.Usb == "" {
				fmt.Fprintf(stdout, "%s\tproduct:%s model:%s device:%s\n",
					device.Serial, device.Product, device.Model, device.DeviceInfo)
			} else {
				fmt.Fprintf(stdout, "%s\tusb:%s product:%s model:%s device:%s\n",
					device.Serial, device.Usb, device.Product, device.Model, device.DeviceInfo)
			}
		} else {
			fmt.Fprintln(stdout, device.Serial)
		}
	}

	return 0
}

func runShell(client cliClient, commandAndArgs []string, device goadb.DeviceDescriptor, stdin io.Reader, stdout, stderr io.Writer) int {
	deviceClient := client.Device(device)
	if len(commandAndArgs) == 0 {
		return runInteractiveShell(deviceClient, stdin, stdout, stderr)
	}

	command := commandAndArgs[0]
	var args []string
	if len(commandAndArgs) > 1 {
		args = commandAndArgs[1:]
	}

	output, err := deviceClient.RunCommand(command, args...)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	fmt.Fprint(stdout, output)
	return 0
}

func runInteractiveShell(device cliDevice, stdin io.Reader, stdout, stderr io.Writer) int {
	usePty := inputIsTerminal(stdin)
	if usePty {
		restore, err := makeInputRaw(stdin)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		defer restore()
	}

	termValue := ""
	if usePty {
		termValue = shellTerm()
	}
	shell, err := device.OpenShell(goadb.ShellOptions{
		Pty:  usePty,
		Term: termValue,
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if usePty {
		if rows, cols, ok := outputWindowSize(stdout); ok {
			_ = shell.SetWindowSize(rows, cols, 0, 0)
		}
	}

	input := stdin
	if !usePty {
		input = newShellInputReader(stdin)
	}

	stdinErrCh := make(chan error, 1)
	stdoutErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(shell, input)
		if !isIgnorableCopyError(err) {
			stdinErrCh <- err
			return
		}
		err = shell.CloseStdin()
		if isIgnorableCopyError(err) {
			stdinErrCh <- nil
			return
		}
		stdinErrCh <- err
	}()
	go func() {
		_, err := io.Copy(stdout, shell)
		stdoutErrCh <- err
	}()

	stdinDone := false
	for {
		select {
		case err := <-stdinErrCh:
			stdinDone = true
			if !isIgnorableCopyError(err) {
				_ = shell.Close()
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
		case err := <-stdoutErrCh:
			_ = shell.Close()
			if !isIgnorableCopyError(err) {
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
			if !stdinDone {
				select {
				case stdinErr := <-stdinErrCh:
					if !isIgnorableCopyError(stdinErr) {
						fmt.Fprintln(stderr, "error:", stdinErr)
						return 1
					}
				default:
				}
			}
			return 0
		}
	}
}

var inputIsTerminal = isInputTerminal
var makeInputRaw = makeTerminalRaw
var outputWindowSize = terminalWindowSize
var shellTerm = func() string {
	return os.Getenv("TERM")
}

func isInputTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func makeTerminalRaw(reader io.Reader) (func(), error) {
	file, ok := reader.(*os.File)
	if !ok {
		return func() {}, nil
	}
	fd := int(file.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() {
		_ = term.Restore(fd, state)
	}, nil
}

func terminalWindowSize(writer io.Writer) (rows, cols int, ok bool) {
	file, ok := writer.(*os.File)
	if !ok {
		return 0, 0, false
	}
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return height, width, true
}

func isIgnorableCopyError(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

type shellInputReader struct {
	reader     *bufio.Reader
	pendingErr error
}

func newShellInputReader(reader io.Reader) io.Reader {
	return &shellInputReader{reader: bufio.NewReader(reader)}
}

func (r *shellInputReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pendingErr != nil {
		err := r.pendingErr
		r.pendingErr = nil
		return 0, err
	}

	n := 0
	for n < len(p) {
		b, err := r.reader.ReadByte()
		if err != nil {
			if n > 0 {
				r.pendingErr = err
				return n, nil
			}
			return 0, err
		}

		if b == '\r' {
			next, err := r.reader.ReadByte()
			if err == nil {
				if next != '\n' {
					_ = r.reader.UnreadByte()
				}
			} else {
				r.pendingErr = err
			}
			b = '\n'
		}

		p[n] = b
		n++
	}
	return n, nil
}

func pull(client cliClient, showProgress bool, positional []string, device goadb.DeviceDescriptor, stdout, stderr io.Writer) int {
	if len(positional) == 0 || len(positional) > 2 {
		fmt.Fprintln(stderr, "error: pull requires remote path and optional local path")
		return 1
	}

	remotePath := positional[0]
	localPath := ""
	if len(positional) == 2 {
		localPath = positional[1]
	}
	if localPath == "" {
		localPath = filepath.Base(remotePath)
	}

	deviceClient := client.Device(device)
	info, err := deviceClient.Stat(remotePath)
	if goadb.HasErrCode(err, goadb.ErrCode(goadb.FileNoExistError)) {
		fmt.Fprintln(stderr, "remote file does not exist:", remotePath)
		return 1
	} else if err != nil {
		fmt.Fprintf(stderr, "error reading remote file %s: %s\n", remotePath, err)
		return 1
	}

	remoteFile, err := deviceClient.OpenRead(remotePath)
	if err != nil {
		fmt.Fprintf(stderr, "error opening remote file %s: %s\n", remotePath, goadb.ErrorWithCauseChain(err))
		return 1
	}
	defer remoteFile.Close()

	var localFile io.WriteCloser
	if localPath == StdIoFilename {
		localFile = noCloseWriter{Writer: stdout}
	} else {
		localFile, err = os.Create(localPath)
		if err != nil {
			fmt.Fprintf(stderr, "error opening local file %s: %s\n", localPath, err)
			return 1
		}
		defer localFile.Close()
	}

	if err := copyWithProgressAndStats(localFile, remoteFile, int(info.Size), showProgress, stderr); err != nil {
		fmt.Fprintln(stderr, "error pulling file:", err)
		return 1
	}
	return 0
}

func push(client cliClient, showProgress bool, positional []string, device goadb.DeviceDescriptor, stdin io.Reader, stderr io.Writer) int {
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "error: push requires local path and remote path")
		return 1
	}

	localPath := positional[0]
	remotePath := positional[1]

	var (
		localFile io.ReadCloser
		size      int
		perms     os.FileMode
		mtime     time.Time
	)
	if localPath == "" || localPath == StdIoFilename {
		localFile = noCloseReader{Reader: stdin}
		perms = os.FileMode(0660)
		mtime = goadb.MtimeOfClose
	} else {
		var err error
		localFile, err = os.Open(localPath)
		if err != nil {
			fmt.Fprintf(stderr, "error opening local file %s: %s\n", localPath, err)
			return 1
		}
		defer localFile.Close()
		info, err := os.Stat(localPath)
		if err != nil {
			fmt.Fprintf(stderr, "error reading local file %s: %s\n", localPath, err)
			return 1
		}
		size = int(info.Size())
		perms = info.Mode().Perm()
		mtime = info.ModTime()
	}

	deviceClient := client.Device(device)
	if localPath != "" && localPath != StdIoFilename {
		resolvedRemotePath, err := resolvePushRemotePath(deviceClient, localPath, remotePath)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		remotePath = resolvedRemotePath
	}

	writer, err := deviceClient.OpenWrite(remotePath, perms, mtime)
	if err != nil {
		fmt.Fprintf(stderr, "error opening remote file %s: %s\n", remotePath, err)
		return 1
	}

	if err := copyWithProgressAndStats(writer, localFile, size, showProgress, stderr); err != nil {
		_ = writer.Close()
		fmt.Fprintln(stderr, "error pushing file:", err)
		return 1
	}
	if err := writer.Close(); err != nil {
		fmt.Fprintln(stderr, "error finishing remote file:", err)
		return 1
	}
	return 0
}

func resolvePushRemotePath(device cliDevice, localPath, remotePath string) (string, error) {
	entry, err := device.Stat(remotePath)
	if goadb.HasErrCode(err, goadb.ErrCode(goadb.FileNoExistError)) {
		return remotePath, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading remote path %s: %s", remotePath, err)
	}
	if entry != nil && entry.Mode.IsDir() {
		return path.Join(remotePath, filepath.Base(localPath)), nil
	}
	return remotePath, nil
}

func install(client cliClient, args []string, device goadb.DeviceDescriptor, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "error: install requires an APK path")
		return 1
	}

	apkPath := args[len(args)-1]
	installArgs := args[:len(args)-1]
	apk, err := openAPK(apkPath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "error opening APK %s: %s\n", apkPath, err)
		return 1
	}
	defer apk.Close()

	if err := client.Device(device).Install(apk, installArgs...); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	fmt.Fprintln(stdout, "Success")
	return 0
}

func openAPK(path string, stdin io.Reader) (*lenReadCloser, error) {
	if path == StdIoFilename {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		return &lenReadCloser{
			Reader: bytes.NewReader(data),
			Closer: noOpCloser{},
			size:   len(data),
		}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &lenReadCloser{
		Reader: file,
		Closer: file,
		size:   int(info.Size()),
	}, nil
}

func forward(client cliClient, args []string, device goadb.DeviceDescriptor, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 1 && args[0] == "--list":
		forwards, err := client.ForwardList()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		for _, item := range forwards {
			fmt.Fprintln(stdout, item.String())
		}
		return 0

	case len(args) == 2 && args[0] == "--remove":
		localKind, localPort, err := parseTCPForwardEndpoint(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if err := client.Device(device).RemovePortForwarding(localKind, localPort); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0

	case len(args) == 1 && args[0] == "--remove-all":
		if err := client.Device(device).RemoveAllPortForwards(); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0

	case len(args) == 2:
		localKind, localPort, err := parseTCPForwardEndpoint(args[0])
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		remoteKind, remotePort, err := parseTCPForwardEndpoint(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if err := client.Device(device).StartPortForwarding(localKind, localPort, remoteKind, remotePort); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0

	default:
		fmt.Fprintln(stderr, "error: forward requires --list, --remove tcp:PORT, --remove-all, or tcp:LOCAL tcp:REMOTE")
		return 1
	}
}

func parseTCPForwardEndpoint(endpoint string) (goadb.ForwardProtocolKind, int, error) {
	if !strings.HasPrefix(endpoint, "tcp:") {
		return goadb.ForwardProtocolKindInvalid, 0, fmt.Errorf("only tcp forward endpoints are supported: %s", endpoint)
	}

	portText := strings.TrimPrefix(endpoint, "tcp:")
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return goadb.ForwardProtocolKindInvalid, 0, fmt.Errorf("invalid tcp forward endpoint %q", endpoint)
	}
	return goadb.ForwardProtocolKindTCP, port, nil
}

// copyWithProgressAndStats copies src to dst.
// If showProgress is true and size is positive, a progress bar is shown.
// After copying, final stats about the transfer speed and size are shown.
// Progress and stats are printed to stderr.
func copyWithProgressAndStats(dst io.Writer, src io.Reader, size int, showProgress bool, stderr io.Writer) error {
	var progress *pb.ProgressBar
	if showProgress && size > 0 {
		progress = pb.New(size)
		progress.Output = stderr
		progress.ShowSpeed = true
		progress.ShowPercent = true
		progress.ShowTimeLeft = true
		progress.SetUnits(pb.U_BYTES)
		progress.Start()
		dst = io.MultiWriter(dst, progress)
	}

	startTime := time.Now()
	copied, err := io.Copy(dst, src)

	if progress != nil {
		progress.Finish()
	}

	if pathErr, ok := err.(*os.PathError); ok {
		if errno, ok := pathErr.Err.(syscall.Errno); ok && errno == syscall.EPIPE {
			err = nil
		}
	}
	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	rate := int64(0)
	if duration > 0 {
		rate = int64(float64(copied) / duration.Seconds())
	}
	fmt.Fprintf(stderr, "%d B/s (%d bytes in %s)\n", rate, copied, duration)

	return nil
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: adb [-s SERIAL] [-L tcp:HOST:PORT|-L tcp:PORT] [-P PORT] <command> [args...]")
	fmt.Fprintln(stderr, "commands: devices, shell, pull, push, install, forward")
}

type noCloseReader struct {
	io.Reader
}

func (r noCloseReader) Close() error {
	return nil
}

type noCloseWriter struct {
	io.Writer
}

func (w noCloseWriter) Close() error {
	return nil
}

type noOpCloser struct{}

func (noOpCloser) Close() error {
	return nil
}

type lenReadCloser struct {
	io.Reader
	io.Closer
	size int
}

func (r *lenReadCloser) Len() int {
	return r.size
}
