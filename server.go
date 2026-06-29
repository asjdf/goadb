package adb

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/asjdf/goadb/internal/errors"
	goadbruntime "github.com/asjdf/goadb/internal/goadbserver/runtime"
	"github.com/asjdf/goadb/pkg"
	"github.com/asjdf/goadb/wire"
)

const (
	AdbExecutableName = "adb"

	// Default port the adb server listens on.
	AdbPort = 5037

	AdbVendorKeyExt = ".adb_key"

	goadbServerProbeTimeout = 500 * time.Millisecond
)

type ServerBackend int

const (
	ServerBackendAuto ServerBackend = iota
	ServerBackendGoadb
	ServerBackendAOSP
)

type ServerConfig struct {
	// Path to the adb executable. If empty, the PATH environment variable will be searched.
	// Used only for ServerBackendAOSP or automatic fallback from ServerBackendAuto.
	PathToAdb string

	// Host and port the adb server is listening on.
	// If not specified, will use the default port on localhost.
	Host string
	Port int

	// KeyPath may be an adb private key file, a directory containing adbkey,
	// or a directory containing a *.adb_key file.
	KeyPath string

	// Backend controls how goadb starts a server when one is not already listening.
	// The zero value prefers the in-process pure-Go server and falls back to AOSP adb.
	Backend ServerBackend

	// Dialer used to connect to the adb server.
	Dialer

	fs               *filesystem
	startGoadbServer goadbServerStarter
}

// Server knows how to start the adb server and connect to it.
type server interface {
	Start() error
	StartDebug() error
	Dial() (*wire.Conn, error)
}

func roundTripSingleResponse(s server, req string) ([]byte, error) {
	conn, err := s.Dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return conn.RoundTripSingleResponse([]byte(req))
}

type realServer struct {
	config ServerConfig

	// Caches Host:Port so they don't have to be concatenated for every dial.
	address string
}

type goadbServerStartArgs struct {
	Address string
	KeyPath string
	Debug   bool
}

type goadbServerStarter func(args goadbServerStartArgs) error

func newServer(config ServerConfig) (server, error) {
	if config.Dialer == nil {
		config.Dialer = tcpDialer{}
	}

	if config.Host == "" {
		config.Host = "localhost"
	}
	if config.Port == 0 {
		config.Port = AdbPort
	}

	var serverListenAddress = fmt.Sprintf("%s:%d", config.Host, config.Port)

	if config.fs == nil {
		config.fs = localFilesystem
	}

	if config.startGoadbServer == nil {
		config.startGoadbServer = defaultGoadbServerManager.Start
	}

	if config.Backend == ServerBackendAOSP {
		adbPath, err := resolveAOSPExecutable(config)
		if err != nil {
			return nil, err
		}
		config.PathToAdb = adbPath
	}

	return &realServer{
		config:  config,
		address: serverListenAddress,
	}, nil
}

// Dial tries to connect to the server. If the first attempt fails, tries starting the server before
// retrying. If the second attempt fails, returns the error.
func (s *realServer) Dial() (*wire.Conn, error) {
	if s.config.Backend != ServerBackendAOSP {
		if err := s.Start(); err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server for dial")
		}
	}

	conn, err := s.config.Dial(s.address)
	if err != nil {
		if err = s.Start(); err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server for dial")
		}

		conn, err = s.config.Dial(s.address)
		if err != nil {
			return nil, err
		}
	}
	return conn, nil
}

// StartServer ensures there is a server running.
func (s *realServer) Start() error {
	return s.start(false)
}

func (s *realServer) StartDebug() error {
	return s.start(true)
}

func (s *realServer) start(debug bool) error {
	switch s.config.Backend {
	case ServerBackendGoadb:
		return s.startGoadb(debug)
	case ServerBackendAOSP:
		return s.startAOSP(debug)
	case ServerBackendAuto:
		goadbErr := s.startGoadb(debug)
		if goadbErr == nil {
			return nil
		}
		aospErr := s.startAOSP(debug)
		if aospErr == nil {
			return nil
		}
		return errors.WrapErrorf(aospErr, errors.ServerNotAvailable,
			"could not start pure-Go goadb-server (%v); AOSP adb fallback also failed", goadbErr)
	default:
		return errors.WrapErrorf(fmt.Errorf("unknown backend %d", s.config.Backend), errors.ParseError, "invalid server backend")
	}
}

func (s *realServer) isListening() bool {
	conn, err := s.config.Dial(s.address)
	if err != nil {
		return false
	}
	if conn == nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *realServer) startGoadb(debug bool) error {
	if s.isGoadbServerListening() {
		return nil
	}
	if s.isListening() {
		_ = s.killExistingServer()
		if err := waitForServerPortClosed(s.address, 5*time.Second); err != nil {
			return err
		}
	}
	return s.config.startGoadbServer(goadbServerStartArgs{
		Address: s.address,
		KeyPath: s.config.KeyPath,
		Debug:   debug,
	})
}

func (s *realServer) startAOSP(debug bool) error {
	if s.isListening() {
		return nil
	}
	if debug {
		return s.startAOSPDebug()
	}

	adbPath, err := resolveAOSPExecutable(s.config)
	if err != nil {
		return err
	}
	s.config.PathToAdb = adbPath
	var envMap = make(map[string]string)
	if s.config.KeyPath != "" {
		envMap["ADB_VENDOR_KEYS"] = s.config.KeyPath
	}
	var cmdArgs = pkg.CommandExecuteArgs{
		Name:    adbPath,
		ArgList: []string{"-L", fmt.Sprintf("tcp:%s", s.address), "start-server"},
		EnvMap:  envMap,
	}
	output, err := s.config.fs.CmdCombinedOutput(cmdArgs)
	outputStr := strings.TrimSpace(string(output))
	return errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server: %s\noutput:\n%s", err, outputStr)
}

func (s *realServer) startAOSPDebug() error {
	if s.isListening() {
		return nil
	}
	adbPath, err := resolveAOSPExecutable(s.config)
	if err != nil {
		return err
	}
	s.config.PathToAdb = adbPath
	var envMap = make(map[string]string)
	envMap["ADB_TRACE"] = "all"
	if s.config.KeyPath != "" {
		envMap["ADB_VENDOR_KEYS"] = s.config.KeyPath
	}
	var cmdArgs = pkg.CommandExecuteArgs{
		Name:    adbPath,
		ArgList: []string{"-L", fmt.Sprintf("tcp:%s", s.address), "server", "nodaemon"},
		EnvMap:  envMap,
	}
	processHolder, err := s.config.fs.CmdWithStream(cmdArgs)
	if err != nil {
		return errors.WrapErrorf(err, errors.ServerNotAvailable, "failed to start server")
	}
	if err := processHolder.RedirectOutputAsync("[AdbServer]"); err != nil {
		return errors.WrapErrorf(err, errors.ServerNotAvailable, "failed to redirect output stream for server")
	}
	return nil
}

func (s *realServer) isGoadbServerListening() bool {
	conn, err := s.config.Dial(s.address)
	if err != nil || conn == nil {
		return false
	}

	done := make(chan bool, 1)
	go func() {
		resp, err := conn.RoundTripSingleResponse([]byte("host:goadb-server"))
		done <- err == nil && string(resp) == "goadb-server"
	}()

	select {
	case result := <-done:
		_ = conn.Close()
		return result
	case <-time.After(goadbServerProbeTimeout):
		_ = conn.Close()
		return false
	}
}

func (s *realServer) killExistingServer() error {
	conn, err := s.config.Dial(s.address)
	if err != nil {
		return err
	}
	defer conn.Close()
	return wire.SendMessageString(conn, "host:kill")
}

func waitForServerPortClosed(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !goadbServerPortOpen(address) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.WrapErrorf(fmt.Errorf("server at %s stayed open", address), errors.ServerNotAvailable, "error stopping existing adb server")
}

func resolveAOSPExecutable(config ServerConfig) (string, error) {
	adbPath := config.PathToAdb
	if adbPath == "" {
		var err error
		adbPath, err = config.fs.LookPath(AdbExecutableName)
		if err != nil {
			return "", errors.WrapErrorf(err, errors.ServerNotAvailable, "could not find %s in PATH", AdbExecutableName)
		}
	}
	if err := config.fs.IsExecutableFile(adbPath); err != nil {
		return "", errors.WrapErrorf(err, errors.ServerNotAvailable, "invalid adb executable: %s", adbPath)
	}
	return adbPath, nil
}

type inProcessGoadbServerManager struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

var defaultGoadbServerManager = &inProcessGoadbServerManager{
	cancels: make(map[string]context.CancelFunc),
}

func (m *inProcessGoadbServerManager) Start(args goadbServerStartArgs) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel := m.cancels[args.Address]; cancel != nil {
		if goadbServerPortOpen(args.Address) {
			return nil
		}
		cancel()
		delete(m.cancels, args.Address)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := goadbruntime.StartBackground(ctx, goadbruntime.Options{
		ListenAddr: args.Address,
		KeyPath:    args.KeyPath,
	}); err != nil {
		cancel()
		return errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting pure-Go goadb-server")
	}
	m.cancels[args.Address] = cancel
	return nil
}

func goadbServerPortOpen(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// filesystem abstracts interactions with the local filesystem for testability.
type filesystem struct {
	// Wraps exec.LookPath.
	LookPath func(string) (string, error)

	ListFileNonRecursive func(dir string) ([]os.FileInfo, error)

	// Returns nil if path is a regular file and executable by the current user.
	IsExecutableFile func(path string) error

	CmdWithStream func(args pkg.CommandExecuteArgs) (processHolder *pkg.ProcessHolder, err error)

	// Wraps exec.Command().CombinedOutput()
	CmdCombinedOutput func(args pkg.CommandExecuteArgs) ([]byte, error)
}
