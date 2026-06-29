package usbwin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

func TestBackendEnumerateFiltersRecognizedADBInterfaces(t *testing.T) {
	api := &fakeAPI{
		names: []string{"not-adb", "adb-device"},
		descriptors: map[string]InterfaceDescriptor{
			"not-adb":    {Class: 0x08, SubClass: 0x06, Protocol: 0x50},
			"adb-device": {Class: ADBClass, SubClass: ADBSubClass, Protocol: ADBProtocol},
		},
		serials: map[string]string{"adb-device": "USB123"},
	}

	backend := New(Options{API: api})
	devices, err := backend.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("Enumerate returned %d devices, want 1", len(devices))
	}
	if devices[0].Name != "adb-device" || devices[0].Serial != "USB123" {
		t.Fatalf("device = %#v", devices[0])
	}
	if !api.closed["not-adb"] {
		t.Fatal("non-adb interface was not closed")
	}
}

func TestBackendEnumerateSkipsInterfacesThatCannotOpen(t *testing.T) {
	api := &fakeAPI{
		names:      []string{"busy-interface", "adb-device"},
		openErrors: map[string]error{"busy-interface": errors.New("in use")},
		descriptors: map[string]InterfaceDescriptor{
			"adb-device": {Class: ADBClass, SubClass: ADBSubClass, Protocol: ADBProtocol},
		},
		serials: map[string]string{"adb-device": "USB123"},
	}

	backend := New(Options{API: api})
	devices, err := backend.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("Enumerate returned %d devices, want 1", len(devices))
	}
	if devices[0].Name != "adb-device" || devices[0].Serial != "USB123" {
		t.Fatalf("device = %#v", devices[0])
	}
}

func TestEndpointConnReadWriteUsesBulkEndpoints(t *testing.T) {
	api := &fakeEndpointAPI{
		readData: bytes.NewBufferString("abc"),
	}
	conn := newEndpointConn(api, handle(1), handle(2), handle(3))

	buf := make([]byte, 2)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if n != 2 || string(buf) != "ab" {
		t.Fatalf("Read = %d %q, want 2 ab", n, string(buf))
	}
	if n, err := conn.Write([]byte("xy")); err != nil || n != 2 {
		t.Fatalf("Write = %d, %v; want 2, nil", n, err)
	}
	if got := api.writeData.String(); got != "xy" {
		t.Fatalf("write endpoint got %q, want xy", got)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if got, want := api.closeOrder, []handle{2, 3, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("close order = %#v, want %#v", got, want)
	}
}

func TestRealEnumerateADBUSBInterfaces(t *testing.T) {
	if os.Getenv("USBWIN_REAL_ENUM") != "1" {
		t.Skip("set USBWIN_REAL_ENUM=1 to enumerate real ADB USB interfaces")
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
	names       []string
	openErrors  map[string]error
	descriptors map[string]InterfaceDescriptor
	serials     map[string]string
	closed      map[string]bool
	handles     map[handle]string
	index       int
	nextHandle  handle
}

func (f *fakeAPI) EnumInterfaces() (handle, error) {
	if f.closed == nil {
		f.closed = make(map[string]bool)
	}
	if f.handles == nil {
		f.handles = make(map[handle]string)
	}
	return 100, nil
}

func (f *fakeAPI) NextInterface(enum handle) (string, bool, error) {
	if f.index >= len(f.names) {
		return "", false, nil
	}
	name := f.names[f.index]
	f.index++
	return name, true, nil
}

func (f *fakeAPI) OpenInterface(name string) (handle, error) {
	if err := f.openErrors[name]; err != nil {
		return 0, err
	}
	f.nextHandle++
	h := f.nextHandle
	f.handles[h] = name
	return h, nil
}

func (f *fakeAPI) InterfaceDescriptor(iface handle) (InterfaceDescriptor, error) {
	return f.descriptors[f.handles[iface]], nil
}

func (f *fakeAPI) SerialNumber(iface handle) (string, error) {
	return f.serials[f.handles[iface]], nil
}

func (f *fakeAPI) OpenReadEndpoint(iface handle) (handle, error) {
	return iface + 1000, nil
}

func (f *fakeAPI) OpenWriteEndpoint(iface handle) (handle, error) {
	return iface + 2000, nil
}

func (f *fakeAPI) Close(h handle) error {
	if name := f.handles[h]; name != "" {
		f.closed[name] = true
	}
	return nil
}

func (f *fakeAPI) ReadEndpoint(h handle, p []byte) (int, error) {
	return 0, io.EOF
}

func (f *fakeAPI) WriteEndpoint(h handle, p []byte) (int, error) {
	return len(p), nil
}

type fakeEndpointAPI struct {
	readData   *bytes.Buffer
	writeData  bytes.Buffer
	closeOrder []handle
}

func (f *fakeEndpointAPI) Close(h handle) error {
	f.closeOrder = append(f.closeOrder, h)
	return nil
}

func (f *fakeEndpointAPI) ReadEndpoint(h handle, p []byte) (int, error) {
	return f.readData.Read(p)
}

func (f *fakeEndpointAPI) WriteEndpoint(h handle, p []byte) (int, error) {
	return f.writeData.Write(p)
}
