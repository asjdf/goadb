package adb

import (
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/asjdf/goadb/internal/errors"
	"github.com/asjdf/goadb/internal/goadbserver/smartsocket"
	"github.com/asjdf/goadb/pkg"
	"github.com/asjdf/goadb/wire"
	"github.com/stretchr/testify/assert"
)

func TestNewServer_ZeroConfigDoesNotRequireADBExecutable(t *testing.T) {
	lookPathCalled := false
	config := ServerConfig{fs: &filesystem{
		LookPath: func(name string) (string, error) {
			lookPathCalled = true
			return "", fmt.Errorf("unexpected lookup: %s", name)
		},
		IsExecutableFile: func(path string) error {
			return fmt.Errorf("unexpected executable check: %s", path)
		},
	}}

	serverIf, err := newServer(config)
	server := serverIf.(*realServer)
	assert.NoError(t, err)
	assert.False(t, lookPathCalled)
	assert.Equal(t, ServerBackendAuto, server.config.Backend)
	assert.IsType(t, tcpDialer{}, server.config.Dialer)
	assert.Equal(t, "localhost", server.config.Host)
	assert.Equal(t, AdbPort, server.config.Port)
	assert.Equal(t, fmt.Sprintf("localhost:%d", AdbPort), server.address)
	assert.Equal(t, "", server.config.PathToAdb)
}

type MockDialer struct{}

func (d MockDialer) Dial(address string) (*wire.Conn, error) {
	return nil, nil
}

type FailingDialer struct{}

func (d FailingDialer) Dial(address string) (*wire.Conn, error) {
	return nil, fmt.Errorf("dial failed: %s", address)
}

func TestNewServer_CustomConfig(t *testing.T) {
	config := ServerConfig{
		Dialer:    MockDialer{},
		Host:      "foobar",
		Port:      1,
		PathToAdb: "/bin/adb",
		Backend:   ServerBackendAOSP,
		fs: &filesystem{
			IsExecutableFile: func(path string) error {
				if path == "/bin/adb" {
					return nil
				}
				return fmt.Errorf("wrong path: %s", path)
			},
		},
	}

	serverIf, err := newServer(config)
	server := serverIf.(*realServer)
	assert.NoError(t, err)
	assert.IsType(t, MockDialer{}, server.config.Dialer)
	assert.Equal(t, "foobar", server.config.Host)
	assert.Equal(t, 1, server.config.Port)
	assert.Equal(t, fmt.Sprintf("foobar:1"), server.address)
	assert.Equal(t, "/bin/adb", server.config.PathToAdb)
}

func TestNewServer_AOSPBackendAdbNotFound(t *testing.T) {
	config := ServerConfig{fs: &filesystem{
		LookPath: func(name string) (string, error) {
			return "", fmt.Errorf("executable not found: %s", name)
		},
	}, Backend: ServerBackendAOSP}

	_, err := newServer(config)
	assert.EqualError(t, err, "ServerNotAvailable: could not find adb in PATH")
}

func TestRealServerStartAutoUsesPureGoBeforeAOSP(t *testing.T) {
	var calls []string
	config := ServerConfig{
		Dialer: FailingDialer{},
		fs: &filesystem{
			LookPath: func(name string) (string, error) {
				calls = append(calls, "lookpath:"+name)
				return "/bin/adb", nil
			},
			IsExecutableFile: func(path string) error {
				calls = append(calls, "executable:"+path)
				return nil
			},
			CmdCombinedOutput: func(args pkg.CommandExecuteArgs) ([]byte, error) {
				calls = append(calls, "aosp:"+args.Name)
				return nil, nil
			},
		},
		startGoadbServer: func(args goadbServerStartArgs) error {
			calls = append(calls, "goadb:"+args.Address)
			return nil
		},
	}

	serverIf, err := newServer(config)
	assert.NoError(t, err)

	err = serverIf.Start()

	assert.NoError(t, err)
	assert.Equal(t, []string{"goadb:localhost:5037"}, calls)
}

func TestRealServerStartAutoFallsBackToAOSPWhenPureGoFails(t *testing.T) {
	var calls []string
	config := ServerConfig{
		Dialer: FailingDialer{},
		fs: &filesystem{
			LookPath: func(name string) (string, error) {
				calls = append(calls, "lookpath:"+name)
				return "/bin/adb", nil
			},
			IsExecutableFile: func(path string) error {
				calls = append(calls, "executable:"+path)
				return nil
			},
			CmdCombinedOutput: func(args pkg.CommandExecuteArgs) ([]byte, error) {
				calls = append(calls, fmt.Sprintf("aosp:%s:%t", args.Name, reflect.DeepEqual(args.ArgList, []string{"-L", "tcp:localhost:5037", "start-server"})))
				return nil, nil
			},
		},
		startGoadbServer: func(args goadbServerStartArgs) error {
			calls = append(calls, "goadb:"+args.Address)
			return errors.WrapErrorf(fmt.Errorf("no adbkey"), errors.ServerNotAvailable, "pure-go unavailable")
		},
	}

	serverIf, err := newServer(config)
	assert.NoError(t, err)

	err = serverIf.Start()

	assert.NoError(t, err)
	assert.Equal(t, []string{
		"goadb:localhost:5037",
		"lookpath:adb",
		"executable:/bin/adb",
		"aosp:/bin/adb:true",
	}, calls)
}

func TestRealServerStartAutoKillsExistingNonGoadbServerBeforeStartingPureGo(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	killed := make(chan struct{}, 1)
	go serveFakeNonGoadbADBServer(t, listener, killed)

	port := listener.Addr().(*net.TCPAddr).Port
	started := false
	serverIf, err := newServer(ServerConfig{
		Host: "127.0.0.1",
		Port: port,
		startGoadbServer: func(args goadbServerStartArgs) error {
			started = true
			assert.Equal(t, fmt.Sprintf("127.0.0.1:%d", port), args.Address)
			return nil
		},
	})
	assert.NoError(t, err)

	err = serverIf.Start()

	assert.NoError(t, err)
	assert.True(t, started)
	select {
	case <-killed:
	case <-time.After(time.Second):
		t.Fatal("existing non-goadb server did not receive host:kill")
	}
}

func TestRealServerIsGoadbServerListeningReturnsWhenPortIsSilent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	server := &realServer{
		config: ServerConfig{
			Dialer: tcpDialer{},
		},
		address: listener.Addr().String(),
	}

	done := make(chan bool, 1)
	go func() {
		done <- server.isGoadbServerListening()
	}()

	select {
	case got := <-done:
		assert.False(t, got)
	case <-time.After(750 * time.Millisecond):
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
		t.Fatal("isGoadbServerListening did not return for a silent listener")
	}
}

func serveFakeNonGoadbADBServer(t *testing.T, listener net.Listener, killed chan<- struct{}) {
	t.Helper()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			request, err := smartsocket.ReadProtocolString(conn)
			if err != nil {
				return
			}
			switch request {
			case "host:goadb-server":
				_ = smartsocket.WriteFAIL(conn, "unsupported")
			case "host:kill":
				_ = smartsocket.WriteOKAY(conn)
				select {
				case killed <- struct{}{}:
				default:
				}
				_ = listener.Close()
			default:
				_ = smartsocket.WriteFAIL(conn, "unexpected "+request)
			}
		}()
	}
}
