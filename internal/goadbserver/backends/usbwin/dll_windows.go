//go:build windows

package usbwin

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	adbOpenAccessTypeReadWrite  uint32 = 0
	adbOpenSharingModeReadWrite uint32 = 0
)

type dllAPI struct {
	once sync.Once
	err  error

	adbEnumInterfaces               uintptr
	adbNextInterface                func(uintptr, unsafe.Pointer, *uint32) bool
	adbCreateInterfaceByName        func(*uint16) uintptr
	adbGetUsbInterfaceDescriptor    func(uintptr, *usbInterfaceDescriptor) bool
	adbGetSerialNumber              func(uintptr, unsafe.Pointer, *uint32, bool) bool
	adbOpenDefaultBulkReadEndpoint  func(uintptr, uint32, uint32) uintptr
	adbOpenDefaultBulkWriteEndpoint func(uintptr, uint32, uint32) uintptr
	adbReadEndpointSync             func(uintptr, unsafe.Pointer, uint32, *uint32, uint32) bool
	adbWriteEndpointSync            func(uintptr, unsafe.Pointer, uint32, *uint32, uint32) bool
	adbCloseHandle                  func(uintptr) bool
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type usbInterfaceDescriptor struct {
	Length            uint8
	DescriptorType    uint8
	InterfaceNumber   uint8
	AlternateSetting  uint8
	NumEndpoints      uint8
	InterfaceClass    uint8
	InterfaceSubClass uint8
	InterfaceProtocol uint8
	Interface         uint8
}

var androidUSBClassID = guid{
	Data1: 0xf72fe0d4,
	Data2: 0xcbcb,
	Data3: 0x407d,
	Data4: [8]byte{0x88, 0x14, 0x9e, 0xd6, 0x73, 0xd0, 0xdd, 0x6b},
}

func defaultAPI() API {
	return &dllAPI{}
}

func (a *dllAPI) load() error {
	a.once.Do(func() {
		lib, err := syscall.LoadLibrary("AdbWinApi.dll")
		if err != nil {
			a.err = err
			return
		}
		addr, err := syscall.GetProcAddress(lib, "AdbEnumInterfaces")
		if err != nil {
			a.err = err
			return
		}
		a.adbEnumInterfaces = addr
		purego.RegisterLibFunc(&a.adbNextInterface, uintptr(lib), "AdbNextInterface")
		purego.RegisterLibFunc(&a.adbCreateInterfaceByName, uintptr(lib), "AdbCreateInterfaceByName")
		purego.RegisterLibFunc(&a.adbGetUsbInterfaceDescriptor, uintptr(lib), "AdbGetUsbInterfaceDescriptor")
		purego.RegisterLibFunc(&a.adbGetSerialNumber, uintptr(lib), "AdbGetSerialNumber")
		purego.RegisterLibFunc(&a.adbOpenDefaultBulkReadEndpoint, uintptr(lib), "AdbOpenDefaultBulkReadEndpoint")
		purego.RegisterLibFunc(&a.adbOpenDefaultBulkWriteEndpoint, uintptr(lib), "AdbOpenDefaultBulkWriteEndpoint")
		purego.RegisterLibFunc(&a.adbReadEndpointSync, uintptr(lib), "AdbReadEndpointSync")
		purego.RegisterLibFunc(&a.adbWriteEndpointSync, uintptr(lib), "AdbWriteEndpointSync")
		purego.RegisterLibFunc(&a.adbCloseHandle, uintptr(lib), "AdbCloseHandle")
	})
	return a.err
}

func (a *dllAPI) EnumInterfaces() (handle, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	h, _, _ := purego.SyscallN(
		a.adbEnumInterfaces,
		uintptr(androidUSBClassID.Data1),
		uintptr(uint32(androidUSBClassID.Data2)|uint32(androidUSBClassID.Data3)<<16),
		uintptr(uint32(androidUSBClassID.Data4[0])|uint32(androidUSBClassID.Data4[1])<<8|uint32(androidUSBClassID.Data4[2])<<16|uint32(androidUSBClassID.Data4[3])<<24),
		uintptr(uint32(androidUSBClassID.Data4[4])|uint32(androidUSBClassID.Data4[5])<<8|uint32(androidUSBClassID.Data4[6])<<16|uint32(androidUSBClassID.Data4[7])<<24),
		1,
		1,
		1,
	)
	if h == 0 {
		return 0, fmt.Errorf("AdbEnumInterfaces returned null")
	}
	return handle(h), nil
}

func (a *dllAPI) NextInterface(enum handle) (string, bool, error) {
	if err := a.load(); err != nil {
		return "", false, err
	}
	buf := make([]byte, 2048)
	size := uint32(len(buf))
	if !a.adbNextInterface(uintptr(enum), unsafe.Pointer(&buf[0]), &size) {
		return "", false, nil
	}
	return interfaceInfoName(buf), true, nil
}

func (a *dllAPI) OpenInterface(name string) (handle, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h := a.adbCreateInterfaceByName(ptr)
	if h == 0 {
		if lastErr := syscall.GetLastError(); lastErr != syscall.Errno(0) {
			return 0, fmt.Errorf("AdbCreateInterfaceByName returned null: %w", lastErr)
		}
		return 0, fmt.Errorf("AdbCreateInterfaceByName returned null")
	}
	return handle(h), nil
}

func (a *dllAPI) InterfaceDescriptor(iface handle) (InterfaceDescriptor, error) {
	if err := a.load(); err != nil {
		return InterfaceDescriptor{}, err
	}
	var desc usbInterfaceDescriptor
	if !a.adbGetUsbInterfaceDescriptor(uintptr(iface), &desc) {
		return InterfaceDescriptor{}, fmt.Errorf("AdbGetUsbInterfaceDescriptor failed")
	}
	return InterfaceDescriptor{
		Class:    desc.InterfaceClass,
		SubClass: desc.InterfaceSubClass,
		Protocol: desc.InterfaceProtocol,
	}, nil
}

func (a *dllAPI) SerialNumber(iface handle) (string, error) {
	if err := a.load(); err != nil {
		return "", err
	}
	buf := make([]byte, 512)
	size := uint32(len(buf))
	if !a.adbGetSerialNumber(uintptr(iface), unsafe.Pointer(&buf[0]), &size, true) {
		return "", fmt.Errorf("AdbGetSerialNumber failed")
	}
	return strings.TrimRight(string(buf[:size]), "\x00"), nil
}

func (a *dllAPI) OpenReadEndpoint(iface handle) (handle, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	h := a.adbOpenDefaultBulkReadEndpoint(uintptr(iface), adbOpenAccessTypeReadWrite, adbOpenSharingModeReadWrite)
	if h == 0 {
		return 0, fmt.Errorf("AdbOpenDefaultBulkReadEndpoint returned null")
	}
	return handle(h), nil
}

func (a *dllAPI) OpenWriteEndpoint(iface handle) (handle, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	h := a.adbOpenDefaultBulkWriteEndpoint(uintptr(iface), adbOpenAccessTypeReadWrite, adbOpenSharingModeReadWrite)
	if h == 0 {
		return 0, fmt.Errorf("AdbOpenDefaultBulkWriteEndpoint returned null")
	}
	return handle(h), nil
}

func (a *dllAPI) Close(h handle) error {
	if h == 0 {
		return nil
	}
	if err := a.load(); err != nil {
		return err
	}
	if !a.adbCloseHandle(uintptr(h)) {
		return fmt.Errorf("AdbCloseHandle failed")
	}
	return nil
}

func (a *dllAPI) ReadEndpoint(h handle, p []byte) (int, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	var read uint32
	if !a.adbReadEndpointSync(uintptr(h), unsafe.Pointer(&p[0]), uint32(len(p)), &read, 0) {
		return int(read), fmt.Errorf("AdbReadEndpointSync failed")
	}
	return int(read), nil
}

func (a *dllAPI) WriteEndpoint(h handle, p []byte) (int, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	var written uint32
	var ptr unsafe.Pointer
	if len(p) > 0 {
		ptr = unsafe.Pointer(&p[0])
	}
	if !a.adbWriteEndpointSync(uintptr(h), ptr, uint32(len(p)), &written, 5000) {
		return int(written), fmt.Errorf("AdbWriteEndpointSync failed")
	}
	return int(written), nil
}

func utf16String(buf []uint16) string {
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(utf16.Decode(buf[:n]))
}

func interfaceInfoName(buf []byte) string {
	const deviceNameOffset = 20
	if len(buf) <= deviceNameOffset {
		return ""
	}
	words := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[deviceNameOffset])), (len(buf)-deviceNameOffset)/2)
	return utf16String(words)
}
