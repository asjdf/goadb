package adb

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/asjdf/goadb/internal/errors"
	"github.com/asjdf/goadb/pkg"
	"github.com/asjdf/goadb/wire"
)

const (
	AdbExecutableName = "adb"

	// Default port the adb server listens on.
	AdbPort = 5037

	AdbVendorKeyExt = ".adb_key"
)

type ServerConfig struct {
	// Path to the adb executable. If empty, the PATH environment variable will be searched.
	PathToAdb string

	// Host and port the adb server is listening on.
	// If not specified, will use the default port on localhost.
	Host string
	Port int
	// KeyPath  must be a dir and file name of the private key must have ext .adb_key
	KeyPath string

	// Dialer used to connect to the adb server.
	Dialer

	fs *filesystem
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

	if config.PathToAdb == "" {
		adbPath, err := config.fs.LookPath(AdbExecutableName)
		if err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "could not find %s in PATH", AdbExecutableName)
		}
		config.PathToAdb = adbPath
	}
	if err := config.fs.IsExecutableFile(config.PathToAdb); err != nil {
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "invalid adb executable: %s", config.PathToAdb)
	}

	if config.KeyPath != "" {
		fileInfoList, err := config.fs.ListFileNonRecursive(config.KeyPath)
		if err != nil {
			return nil, errors.WrapErrorf(err, errors.ParseError, "failed to list file on key path: %s", config.KeyPath)
		}
		var hasMatchedKeyFile = false
		for _, fileInfo := range fileInfoList {
			var fileName = fileInfo.Name()
			if path.Ext(fileName) == AdbVendorKeyExt {
				hasMatchedKeyFile = true
				break
			}
		}
		if !hasMatchedKeyFile {
			return nil, errors.WrapErrorf(err, errors.ParseError, "key file on key path must have ext %s: %s", AdbVendorKeyExt, config.KeyPath)
		}
	}

	return &realServer{
		config:  config,
		address: serverListenAddress,
	}, nil
}

// Dial tries to connect to the server. If the first attempt fails, tries starting the server before
// retrying. If the second attempt fails, returns the error.
func (s *realServer) Dial() (*wire.Conn, error) {
	conn, err := s.config.Dial(s.address)
	if err != nil {
		// Attempt to start the server and try again.
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
	var envMap = make(map[string]string)
	if s.config.KeyPath != "" {
		envMap["ADB_VENDOR_KEYS"] = s.config.KeyPath
	}
	var cmdArgs = pkg.CommandExecuteArgs{
		Name:    s.config.PathToAdb,
		ArgList: []string{"-L", fmt.Sprintf("tcp:%s", s.address), "start-server"},
		EnvMap:  envMap,
	}
	output, err := s.config.fs.CmdCombinedOutput(cmdArgs)
	outputStr := strings.TrimSpace(string(output))
	return errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server: %s\noutput:\n%s", err, outputStr)
}

func (s *realServer) StartDebug() error {
	var envMap = make(map[string]string)
	envMap["ADB_TRACE"] = "all"
	if s.config.KeyPath != "" {
		envMap["ADB_VENDOR_KEYS"] = s.config.KeyPath
	}
	var cmdArgs = pkg.CommandExecuteArgs{
		Name:    s.config.PathToAdb,
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
