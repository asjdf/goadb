package winusb

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

func TestBackendEnumerateRecognizesADBInterfaceAndSelectsBulkPipes(t *testing.T) {
	api := &fakeAPI{
		paths: []string{"not-adb", "adb-device"},
		descriptors: map[string]InterfaceDescriptor{
			"not-adb":    {Class: 0x08, SubClass: 0x06, Protocol: 0x50, NumEndpoints: 2},
			"adb-device": {Class: ADBClass, SubClass: ADBSubClass, Protocol: ADBProtocol, NumEndpoints: 2},
		},
		pipeInfos: map[string][]PipeInfo{
			"adb-device": {
				{PipeType: PipeTypeInterrupt, PipeID: 0x81, MaxPacketSize: 16},
				{PipeType: PipeTypeBulk, PipeID: 0x81, MaxPacketSize: 512},
				{PipeType: PipeTypeBulk, PipeID: 0x02, MaxPacketSize: 512},
			},
		},
		serials: map[string]string{"adb-device": "USB123"},
	}

	devices, err := New(Options{API: api}).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("Enumerate returned %d devices, want 1", len(devices))
	}
	device := devices[0]
	if device.Name != "adb-device" || device.Serial != "USB123" {
		t.Fatalf("device = %#v, want adb-device USB123", device)
	}
	conn := device.Conn.(*endpointConn)
	if conn.readPipe != 0x81 || conn.writePipe != 0x02 || conn.maxPacketSize != 512 {
		t.Fatalf("pipes = read %#x write %#x max %d", conn.readPipe, conn.writePipe, conn.maxPacketSize)
	}
	if !api.closed["not-adb"] {
		t.Fatal("non-adb interface was not closed")
	}
	if got, want := api.resets, []byte{0x81, 0x02}; !equalBytes(got, want) {
		t.Fatalf("resets = %#v, want %#v", got, want)
	}
	if got, want := api.flushes, []byte{0x81}; !equalBytes(got, want) {
		t.Fatalf("flushes = %#v, want %#v", got, want)
	}
}

func TestBackendEnumerateFallsBackToDevicePathSerial(t *testing.T) {
	path := `\\?\usb#vid_0e8d&pid_201c#27414#84UM01439#{f72fe0d4-cbcb-407d-8814-9ed673d0dd6b}`
	api := &fakeAPI{
		paths: []string{path},
		descriptors: map[string]InterfaceDescriptor{
			path: {Class: ADBClass, SubClass: ADBSubClass, Protocol: ADBProtocol, NumEndpoints: 2},
		},
		pipeInfos: map[string][]PipeInfo{
			path: {
				{PipeType: PipeTypeBulk, PipeID: 0x81, MaxPacketSize: 512},
				{PipeType: PipeTypeBulk, PipeID: 0x02, MaxPacketSize: 512},
			},
		},
	}

	devices, err := New(Options{API: api}).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("Enumerate returned %d devices, want 1", len(devices))
	}
	if got, want := devices[0].Serial, "27414/84UM01439"; got != want {
		t.Fatalf("serial = %q, want %q", got, want)
	}
}

func TestEndpointConnWritesZeroLengthPacketForPacketAlignedWrite(t *testing.T) {
	api := &fakeAPI{handles: map[handle]string{1: "adb-device"}}
	conn := newEndpointConn(api, 1, 0x81, 0x02, 4)

	n, err := conn.Write([]byte("abcd"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 4 {
		t.Fatalf("Write returned %d, want 4", n)
	}
	if got, want := api.writePayloads(0x02), []string{"abcd", ""}; !equalStrings(got, want) {
		t.Fatalf("write payloads = %#v, want %#v", got, want)
	}
}

func TestEndpointConnDoesNotWriteZeroLengthPacketForShortWrite(t *testing.T) {
	api := &fakeAPI{handles: map[handle]string{1: "adb-device"}}
	conn := newEndpointConn(api, 1, 0x81, 0x02, 4)

	n, err := conn.Write([]byte("abc"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 3 {
		t.Fatalf("Write returned %d, want 3", n)
	}
	if got, want := api.writePayloads(0x02), []string{"abc"}; !equalStrings(got, want) {
		t.Fatalf("write payloads = %#v, want %#v", got, want)
	}
}

func TestEndpointConnClosesDeviceHandle(t *testing.T) {
	api := &fakeAPI{handles: map[handle]string{1: "adb-device"}}
	conn := newEndpointConn(api, 1, 0x81, 0x02, 512)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !api.closed["adb-device"] {
		t.Fatal("device handle was not closed")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestDecodeUSBStringDescriptor(t *testing.T) {
	got, ok := DecodeUSBStringDescriptor([]byte{10, 3, 'A', 0, 'B', 0, '/', 0, '1', 0})
	if !ok || got != "AB/1" {
		t.Fatalf("DecodeUSBStringDescriptor = %q %v, want AB/1 true", got, ok)
	}
	if got, ok := DecodeUSBStringDescriptor([]byte{4, 2, 'A', 0}); ok || got != "" {
		t.Fatalf("invalid descriptor decoded as %q %v", got, ok)
	}
}

func TestRealEnumerateADBUSBInterfaces(t *testing.T) {
	if os.Getenv("WINUSB_REAL_ENUM") != "1" {
		t.Skip("set WINUSB_REAL_ENUM=1 to enumerate real ADB USB interfaces")
	}
	api := defaultAPI()
	paths, err := api.DevicePaths()
	if err != nil {
		t.Fatalf("DevicePaths returned error: %v", err)
	}
	for _, path := range paths {
		t.Logf("device path=%q", path)
		h, err := api.Open(path)
		if err != nil {
			t.Logf("  open: %v", err)
			continue
		}
		desc, err := api.InterfaceDescriptor(h)
		if err != nil {
			t.Logf("  descriptor: %v", err)
			_ = api.Close(h)
			continue
		}
		pipes, err := api.PipeInfos(h)
		if err != nil {
			t.Logf("  pipes: %v", err)
			_ = api.Close(h)
			continue
		}
		serial, err := api.SerialNumber(h)
		if err != nil {
			t.Logf("  serial: %v", err)
		}
		t.Logf("  desc=%#v pipes=%#v serial=%q fallback=%q", desc, pipes, serial, SerialFromDevicePath(path))
		_ = api.Close(h)
	}
	devices, err := New(Options{}).Enumerate()
	if err != nil {
		t.Fatalf("Enumerate returned error: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("Enumerate returned no devices")
	}
	for _, device := range devices {
		t.Logf("device name=%q serial=%q", device.Name, device.Serial)
		_ = device.Conn.Close()
	}
}

type fakeAPI struct {
	paths       []string
	openErrors  map[string]error
	descriptors map[string]InterfaceDescriptor
	pipeInfos   map[string][]PipeInfo
	serials     map[string]string
	closed      map[string]bool
	handles     map[handle]string
	index       int
	nextHandle  handle
	readData    bytes.Buffer
	writes      []pipeWrite
	resets      []byte
	flushes     []byte
}

type pipeWrite struct {
	pipeID  byte
	payload string
}

func (f *fakeAPI) DevicePaths() ([]string, error) {
	return f.paths, nil
}

func (f *fakeAPI) Open(path string) (handle, error) {
	if err := f.openErrors[path]; err != nil {
		return 0, err
	}
	if f.handles == nil {
		f.handles = make(map[handle]string)
	}
	f.nextHandle++
	h := f.nextHandle
	f.handles[h] = path
	return h, nil
}

func (f *fakeAPI) InterfaceDescriptor(h handle) (InterfaceDescriptor, error) {
	return f.descriptors[f.handles[h]], nil
}

func (f *fakeAPI) PipeInfos(h handle) ([]PipeInfo, error) {
	return f.pipeInfos[f.handles[h]], nil
}

func (f *fakeAPI) SerialNumber(h handle) (string, error) {
	if value, ok := f.serials[f.handles[h]]; ok {
		return value, nil
	}
	return "", errors.New("serial descriptor unavailable")
}

func (f *fakeAPI) Close(h handle) error {
	if f.closed == nil {
		f.closed = make(map[string]bool)
	}
	if path := f.handles[h]; path != "" {
		f.closed[path] = true
	}
	return nil
}

func (f *fakeAPI) ReadPipe(h handle, pipeID byte, p []byte) (int, error) {
	if f.readData.Len() == 0 {
		return 0, io.EOF
	}
	return f.readData.Read(p)
}

func (f *fakeAPI) WritePipe(h handle, pipeID byte, p []byte) (int, error) {
	f.writes = append(f.writes, pipeWrite{pipeID: pipeID, payload: string(p)})
	return len(p), nil
}

func (f *fakeAPI) ResetPipe(h handle, pipeID byte) error {
	f.resets = append(f.resets, pipeID)
	return nil
}

func (f *fakeAPI) FlushPipe(h handle, pipeID byte) error {
	f.flushes = append(f.flushes, pipeID)
	return nil
}

func (f *fakeAPI) writePayloads(pipeID byte) []string {
	var payloads []string
	for _, write := range f.writes {
		if write.pipeID == pipeID {
			payloads = append(payloads, write.payload)
		}
	}
	return payloads
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
