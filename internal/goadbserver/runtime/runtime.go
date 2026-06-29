package runtime

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	osuser "os/user"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/asjdf/goadb/internal/goadbserver/auth"
	tcpbackend "github.com/asjdf/goadb/internal/goadbserver/backends/tcp"
	"github.com/asjdf/goadb/internal/goadbserver/backends/usbwin"
	winusbbackend "github.com/asjdf/goadb/internal/goadbserver/backends/winusb"
	"github.com/asjdf/goadb/internal/goadbserver/forward"
	adbserver "github.com/asjdf/goadb/internal/goadbserver/server"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

const defaultListenAddr = "localhost:5037"

var usbConnectTimeout = 3 * time.Second

type Mode int

const (
	ModeStartServer Mode = iota
	ModeNoDaemon
)

type Config struct {
	ListenAddr string
	Mode       Mode
	KeyPath    string
	InitialTCP []string
}

type Options struct {
	ListenAddr string
	KeyPath    string
	InitialTCP []string
}

type serverMode = Mode
type config = Config

const (
	modeStartServer = ModeStartServer
	modeNoDaemon    = ModeNoDaemon
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func ParseConfig(args []string, home string) (Config, error) {
	return parseConfig(args, home)
}

func parseConfig(args []string, home string) (config, error) {
	var initialTCP stringList
	cfg := config{
		ListenAddr: "tcp:" + defaultListenAddr,
		KeyPath:    defaultKeyPath(home),
	}

	fs := flag.NewFlagSet("goadb-server", flag.ContinueOnError)
	fs.StringVar(&cfg.ListenAddr, "L", "tcp:"+defaultListenAddr, "ADB server listen spec")
	fs.StringVar(&cfg.KeyPath, "key", cfg.KeyPath, "adb private key path")
	fs.Var(&initialTCP, "connect", "initial TCP adb device address")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	listenAddr, err := parseListenSpec(cfg.ListenAddr)
	if err != nil {
		return config{}, err
	}
	cfg.ListenAddr = listenAddr
	cfg.InitialTCP = []string(initialTCP)

	remaining := fs.Args()
	switch {
	case len(remaining) == 1 && remaining[0] == "start-server":
		cfg.Mode = modeStartServer
	case len(remaining) == 2 && remaining[0] == "server" && remaining[1] == "nodaemon":
		cfg.Mode = modeNoDaemon
	default:
		return config{}, fmt.Errorf("unsupported command %q", strings.Join(remaining, " "))
	}
	return cfg, nil
}

func ParseListenSpec(spec string) (string, error) {
	return parseListenSpec(spec)
}

func parseListenSpec(spec string) (string, error) {
	if spec == "" {
		return defaultListenAddr, nil
	}
	if !strings.HasPrefix(spec, "tcp:") {
		return "", fmt.Errorf("unsupported listen spec %q", spec)
	}
	addr := strings.TrimPrefix(spec, "tcp:")
	if !strings.Contains(addr, ":") {
		addr = "localhost:" + addr
	}
	return addr, nil
}

func UserHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		return home
	}
	current, err := osuser.Current()
	if err == nil {
		return current.HomeDir
	}
	return ""
}

func DefaultKeyPath(home string) string {
	return defaultKeyPath(home)
}

func defaultKeyPath(home string) string {
	if keyPath := keyPathFromADBVendorKeys(os.Getenv("ADB_VENDOR_KEYS")); keyPath != "" {
		return keyPath
	}
	if home != "" {
		return strings.TrimRight(home, `/\`) + "/.android/adbkey"
	}
	return ".android/adbkey"
}

func KeyPathFromADBVendorKeys(value string) string {
	return keyPathFromADBVendorKeys(value)
}

func keyPathFromADBVendorKeys(value string) string {
	for _, entry := range filepath.SplitList(value) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyPath, err := ResolveKeyPath(entry)
		if err == nil {
			return keyPath
		}
	}
	return ""
}

func ResolveKeyPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultKeyPath(UserHomeDir())
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("resolve adb key %q: %w", path, err)
	}
	if !info.IsDir() {
		return path, nil
	}

	adbKey := filepath.Join(path, "adbkey")
	if info, err := os.Stat(adbKey); err == nil && !info.IsDir() {
		return adbKey, nil
	}
	matches, err := filepath.Glob(filepath.Join(path, "*.adb_key"))
	if err != nil {
		return "", err
	}
	if len(matches) > 0 {
		return matches[0], nil
	}
	return "", fmt.Errorf("adb key directory %q does not contain adbkey or *.adb_key", path)
}

func PrependDLLSearchPath() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exeDir := filepath.Dir(exe)
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	_ = os.Setenv("PATH", dllSearchPath(os.Getenv("PATH"), exeDir, cwd))
}

func dllSearchPath(existing, exeDir, cwd string) string {
	seen := make(map[string]bool)
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		dirs = append(dirs, dir)
	}
	add(exeDir)
	add(cwd)
	for _, dir := range filepath.SplitList(existing) {
		add(dir)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

func Run(ctx context.Context, opts Options) error {
	cfg, err := normalizeOptions(opts)
	if err != nil {
		return err
	}
	return runNoDaemon(ctx, config{
		ListenAddr: cfg.ListenAddr,
		KeyPath:    cfg.KeyPath,
		InitialTCP: cfg.InitialTCP,
		Mode:       modeNoDaemon,
	})
}

func StartBackground(ctx context.Context, opts Options) error {
	cfg, err := normalizeOptions(opts)
	if err != nil {
		return err
	}
	if tcpPortOpen(cfg.ListenAddr, 200*time.Millisecond) {
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg)
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ticker.C:
			if tcpPortOpen(cfg.ListenAddr, 200*time.Millisecond) {
				return nil
			}
		case <-deadline.C:
			return fmt.Errorf("server did not start listening on %s", cfg.ListenAddr)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func normalizeOptions(opts Options) (Options, error) {
	listenAddr := opts.ListenAddr
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	if !strings.HasPrefix(listenAddr, "tcp:") {
		listenAddr = "tcp:" + listenAddr
	}
	parsed, err := parseListenSpec(listenAddr)
	if err != nil {
		return Options{}, err
	}
	keyPath, err := ResolveKeyPath(opts.KeyPath)
	if err != nil {
		return Options{}, err
	}
	return Options{
		ListenAddr: parsed,
		KeyPath:    keyPath,
		InitialTCP: append([]string(nil), opts.InitialTCP...),
	}, nil
}

func tcpPortOpen(address string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func runNoDaemon(ctx context.Context, cfg config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	keyPair, err := auth.LoadKeyPair(cfg.KeyPath)
	if err != nil {
		return err
	}

	registry := transport.NewRegistry()
	tcpBackend := tcpbackend.New(tcpbackend.Options{KeyPair: keyPair})
	opener := newCompositeOpener(tcpBackend)
	forwardManager := forward.New(opener)
	srv := adbserver.New(registry, adbserver.Options{
		Connector:      tcpBackend,
		Opener:         opener,
		ForwardManager: forwardManager,
		Shutdown:       cancel,
	})

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	log.Printf("goadb server listening on %s", cfg.ListenAddr)

	usbBackends := usbEnumerators()
	scanUSB := func() {
		for _, backend := range usbBackends {
			if err := opener.ScanUSB(ctx, backend.enumerator, registry, keyPair); err != nil {
				log.Printf("usb scan %s: %v", backend.name, err)
			}
		}
	}
	go func() {
		scanUSB()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				scanUSB()
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for _, address := range cfg.InitialTCP {
			info, err := tcpBackend.Connect(ctx, address)
			if err != nil {
				log.Printf("connect %s: %v", address, err)
				continue
			}
			registry.Upsert(info)
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			if err := srv.ServeConn(ctx, conn); err != nil && err != io.EOF {
				log.Printf("client: %v", err)
			}
		}()
	}
}

type compositeOpener struct {
	tcp *tcpbackend.Backend

	mu  sync.Mutex
	usb map[string]*transport.Engine
}

func newCompositeOpener(tcpBackend *tcpbackend.Backend) *compositeOpener {
	return &compositeOpener{tcp: tcpBackend, usb: make(map[string]*transport.Engine)}
}

func (o *compositeOpener) Open(ctx context.Context, serial string, service string) (io.ReadWriteCloser, error) {
	o.mu.Lock()
	usbEngine := o.usb[serial]
	o.mu.Unlock()
	if usbEngine != nil {
		return usbEngine.Open(ctx, service)
	}
	if o.tcp == nil {
		return nil, transport.ErrTransportNotFound
	}
	return o.tcp.Open(ctx, serial, service)
}

type usbDevice struct {
	Name   string
	Serial string
	Conn   io.ReadWriteCloser
}

type usbEnumerator interface {
	Enumerate() ([]usbDevice, error)
}

type namedUSBEnumerator struct {
	name       string
	enumerator usbEnumerator
}

type winusbEnumerator struct {
	backend *winusbbackend.Backend
}

func (e winusbEnumerator) Enumerate() ([]usbDevice, error) {
	devices, err := e.backend.Enumerate()
	if err != nil {
		return nil, err
	}
	out := make([]usbDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, usbDevice{Name: device.Name, Serial: device.Serial, Conn: device.Conn})
	}
	return out, nil
}

type adbWinAPIEnumerator struct {
	backend *usbwin.Backend
}

func (e adbWinAPIEnumerator) Enumerate() ([]usbDevice, error) {
	devices, err := e.backend.Enumerate()
	if err != nil {
		return nil, err
	}
	out := make([]usbDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, usbDevice{Name: device.Name, Serial: device.Serial, Conn: device.Conn})
	}
	return out, nil
}

var usbEnumerators = func() []namedUSBEnumerator {
	backends := []namedUSBEnumerator{{
		name:       "winusb",
		enumerator: winusbEnumerator{backend: winusbbackend.New(winusbbackend.Options{})},
	}}
	if goruntime.GOARCH == "386" {
		backends = append(backends, namedUSBEnumerator{
			name:       "adbwinapi",
			enumerator: adbWinAPIEnumerator{backend: usbwin.New(usbwin.Options{})},
		})
	}
	return backends
}

func (o *compositeOpener) ScanUSB(ctx context.Context, backend usbEnumerator, registry *transport.Registry, keyPair *auth.KeyPair) error {
	devices, err := backend.Enumerate()
	if err != nil {
		return err
	}
	for _, device := range devices {
		o.mu.Lock()
		_, known := o.usb[device.Serial]
		o.mu.Unlock()
		if known {
			_ = device.Conn.Close()
			continue
		}

		engine := transport.NewEngine(device.Conn, transport.Options{
			Serial:  device.Serial,
			Type:    transport.TypeUSB,
			KeyPair: keyPair,
		})
		info, err := connectUSBEngine(ctx, engine)
		if err != nil {
			_ = device.Conn.Close()
			log.Printf("usb %s connect: %v", device.Serial, err)
			continue
		}
		info.DevPath = device.Name

		o.mu.Lock()
		o.usb[device.Serial] = engine
		o.mu.Unlock()
		registry.Upsert(info)
	}
	return nil
}

func connectUSBEngine(ctx context.Context, engine *transport.Engine) (transport.Info, error) {
	connectCtx, cancel := context.WithTimeout(ctx, usbConnectTimeout)
	defer cancel()

	type result struct {
		info transport.Info
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		info, err := engine.Connect(connectCtx)
		resultCh <- result{info: info, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.info, result.err
	case <-connectCtx.Done():
		_ = engine.Close()
		return transport.Info{}, connectCtx.Err()
	}
}
