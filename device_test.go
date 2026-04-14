package adb

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/asjdf/goadb/internal/errors"
	"github.com/asjdf/goadb/wire"
	"github.com/stretchr/testify/assert"
)

func TestGetAttribute(t *testing.T) {
	s := &MockServer{
		Status:   wire.StatusSuccess,
		Messages: []string{"value"},
	}
	client := (&Adb{s}).Device(DeviceWithSerial("serial"))

	v, err := client.getAttribute("attr")
	assert.Equal(t, "host-serial:serial:attr", s.Requests[0])
	assert.NoError(t, err)
	assert.Equal(t, "value", v)
}

func TestGetDeviceInfo(t *testing.T) {
	deviceLister := func() ([]*DeviceInfo, error) {
		return []*DeviceInfo{
			&DeviceInfo{
				Serial:  "abc",
				Product: "Foo",
			},
			&DeviceInfo{
				Serial:  "def",
				Product: "Bar",
			},
		}, nil
	}

	client := newDeviceClientWithDeviceLister("abc", deviceLister)
	device, err := client.DeviceInfo()
	assert.NoError(t, err)
	assert.Equal(t, "Foo", device.Product)

	client = newDeviceClientWithDeviceLister("def", deviceLister)
	device, err = client.DeviceInfo()
	assert.NoError(t, err)
	assert.Equal(t, "Bar", device.Product)

	client = newDeviceClientWithDeviceLister("serial", deviceLister)
	device, err = client.DeviceInfo()
	assert.True(t, HasErrCode(err, DeviceNotFound))
	assert.EqualError(t, err.(*errors.Err).Cause,
		"DeviceNotFound: device list doesn't contain serial serial")
	assert.Nil(t, device)
}

func newDeviceClientWithDeviceLister(serial string, deviceLister func() ([]*DeviceInfo, error)) *Device {
	client := (&Adb{&MockServer{
		Status:   wire.StatusSuccess,
		Messages: []string{serial},
	}}).Device(DeviceWithSerial(serial))
	client.deviceListFunc = deviceLister
	return client
}

func TestRunCommandNoArgs(t *testing.T) {
	s := &MockServer{
		Status:   wire.StatusSuccess,
		Messages: []string{"output"},
	}
	client := (&Adb{s}).Device(AnyDevice())

	v, err := client.RunCommand("cmd")
	assert.Equal(t, "host:transport-any", s.Requests[0])
	assert.Equal(t, "shell:cmd", s.Requests[1])
	assert.NoError(t, err)
	assert.Equal(t, "output", v)
}

func TestInstall(t *testing.T) {
	conn := &installTestConn{
		status: wire.StatusSuccess,
		resp:   []byte("Success"),
	}
	client := (&Adb{&installTestServer{conn: conn}}).Device(AnyDevice())

	apk := bytes.NewReader([]byte("apk"))
	err := client.Install(apk, "-r")

	assert.NoError(t, err)
	assert.Equal(t, []string{
		"host:transport-any",
		"exec:cmd package install -r -S 3",
	}, conn.requests)
	assert.Equal(t, [][]byte{[]byte("apk")}, conn.writes)
}

func TestInstallWithContextCancelStopsInstall(t *testing.T) {
	conn := &installTestConn{
		status:     wire.StatusSuccess,
		blockWrite: true,
	}
	client := (&Adb{&installTestServer{conn: conn}}).Device(AnyDevice())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		apk := bytes.NewReader([]byte("apk"))
		errCh <- client.InstallWithContext(ctx, apk)
	}()

	<-conn.writeStartedChan()
	cancel()
	err := <-errCh

	assert.Error(t, err)
	assert.True(t, HasErrCode(err, NetworkError))

	topErr := err.(*errors.Err)
	assert.Equal(t, "error performing InstallApk on *adb.Device", topErr.Message)

	causeErr, ok := topErr.Cause.(*errors.Err)
	assert.True(t, ok)
	assert.Equal(t, "install canceled", causeErr.Message)
	assert.Equal(t, context.Canceled, causeErr.Cause)
}

func TestPrepareCommandLineNoArgs(t *testing.T) {
	result, err := prepareCommandLine("cmd")
	assert.NoError(t, err)
	assert.Equal(t, "cmd", result)
}

func TestPrepareCommandLineEmptyCommand(t *testing.T) {
	_, err := prepareCommandLine("")
	assert.Equal(t, errors.AssertionError, code(err))
	assert.Equal(t, "command cannot be empty", message(err))
}

func TestPrepareCommandLineBlankCommand(t *testing.T) {
	_, err := prepareCommandLine("  ")
	assert.Equal(t, errors.AssertionError, code(err))
	assert.Equal(t, "command cannot be empty", message(err))
}

func TestPrepareCommandLineCleanArgs(t *testing.T) {
	result, err := prepareCommandLine("cmd", "arg1", "arg2")
	assert.NoError(t, err)
	assert.Equal(t, "cmd arg1 arg2", result)
}

func TestPrepareCommandLineArgWithWhitespaceQuotes(t *testing.T) {
	result, err := prepareCommandLine("cmd", "arg with spaces")
	assert.NoError(t, err)
	assert.Equal(t, "cmd \"arg with spaces\"", result)
}

func TestPrepareCommandLineArgWithDoubleQuoteFails(t *testing.T) {
	_, err := prepareCommandLine("cmd", "quoted\"arg")
	assert.Equal(t, errors.ParseError, code(err))
	assert.Equal(t, "arg at index 0 contains an invalid double quote: quoted\"arg", message(err))
}

func code(err error) errors.ErrCode {
	return err.(*errors.Err).Code
}

func message(err error) string {
	return err.(*errors.Err).Message
}

type installTestServer struct {
	conn *installTestConn
}

func (s *installTestServer) Start() error {
	return nil
}

func (s *installTestServer) StartDebug() error {
	return nil
}

func (s *installTestServer) Dial() (*wire.Conn, error) {
	return wire.NewConn(s.conn, s.conn), nil
}

type installTestConn struct {
	status     string
	resp       []byte
	blockWrite bool

	requests []string
	writes   [][]byte

	closeOnce        sync.Once
	closedInitOnce   sync.Once
	closed           chan struct{}
	writeStartedOnce sync.Once
	writeStartedInit sync.Once
	writeStarted     chan struct{}
}

func (c *installTestConn) Read(p []byte) (int, error) {
	return 0, errors.WrapErrorf(io.EOF, errors.NetworkError, "")
}

func (c *installTestConn) ReadStatus(req string) (string, error) {
	return c.status, nil
}

func (c *installTestConn) ReadMessage() ([]byte, error) {
	return nil, errors.WrapErrorf(io.EOF, errors.NetworkError, "")
}

func (c *installTestConn) ReadUntilEof() ([]byte, error) {
	select {
	case <-c.closedChan():
		return nil, errors.WrapErrorf(io.ErrClosedPipe, errors.NetworkError, "read interrupted")
	default:
	}

	return c.resp, nil
}

func (c *installTestConn) SendMessage(msg []byte) error {
	c.requests = append(c.requests, string(msg))
	return nil
}

func (c *installTestConn) Write(p []byte) (int, error) {
	if c.blockWrite {
		c.writeStartedOnce.Do(func() {
			close(c.writeStartedChan())
		})
		<-c.closedChan()
		return 0, errors.WrapErrorf(io.ErrClosedPipe, errors.NetworkError, "write interrupted")
	}

	c.writes = append(c.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (c *installTestConn) NewSyncScanner() wire.SyncScanner {
	return nil
}

func (c *installTestConn) NewSyncSender() wire.SyncSender {
	return nil
}

func (c *installTestConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closedChan())
	})
	return nil
}

func (c *installTestConn) closedChan() chan struct{} {
	c.closedInitOnce.Do(func() {
		c.closed = make(chan struct{})
	})
	return c.closed
}

func (c *installTestConn) writeStartedChan() chan struct{} {
	c.writeStartedInit.Do(func() {
		c.writeStarted = make(chan struct{})
	})
	return c.writeStarted
}
