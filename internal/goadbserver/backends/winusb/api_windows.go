//go:build windows

package winusb

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	digcfPresent         uint32 = 0x00000002
	digcfDeviceInterface uint32 = 0x00000010

	genericRead  uint32 = 0x80000000
	genericWrite uint32 = 0x40000000

	fileShareRead  uint32 = 0x00000001
	fileShareWrite uint32 = 0x00000002

	openExisting       uint32 = 3
	fileFlagOverlapped uint32 = 0x40000000

	usbDeviceDescriptorType uint8  = 1
	usbStringDescriptorType uint8  = 3
	defaultLanguageID       uint16 = 0x0409
)

const invalidHandleValue = ^uintptr(0)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var androidUSBClassID = guid{
	Data1: 0xf72fe0d4,
	Data2: 0xcbcb,
	Data3: 0x407d,
	Data4: [8]byte{0x88, 0x14, 0x9e, 0xd6, 0x73, 0xd0, 0xdd, 0x6b},
}

var usbDeviceInterfaceGUID = guid{
	Data1: 0xa5dcbf10,
	Data2: 0x6530,
	Data3: 0x11d2,
	Data4: [8]byte{0x90, 0x1f, 0x00, 0xc0, 0x4f, 0xb9, 0x51, 0xed},
}

func deviceInterfaceClasses() []guid {
	return []guid{androidUSBClassID, usbDeviceInterfaceGUID}
}

type spDeviceInterfaceData struct {
	CBSize             uint32
	InterfaceClassGuid guid
	Flags              uint32
	Reserved           uintptr
}

type spDeviceInterfaceDetailData struct {
	CBSize     uint32
	DevicePath [1]uint16
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

type usbDeviceDescriptor struct {
	Length            uint8
	DescriptorType    uint8
	USB               uint16
	DeviceClass       uint8
	DeviceSubClass    uint8
	DeviceProtocol    uint8
	MaxPacketSize0    uint8
	VendorID          uint16
	ProductID         uint16
	Device            uint16
	ManufacturerIndex uint8
	ProductIndex      uint8
	SerialNumberIndex uint8
	NumConfigurations uint8
}

type winusbPipeInformation struct {
	PipeType          uint32
	PipeID            byte
	_                 byte
	MaximumPacketSize uint16
	Interval          byte
	_                 [3]byte
}

type openedDevice struct {
	file uintptr
	usb  uintptr
	path string
}

type windowsAPI struct {
	once sync.Once
	err  error

	setupDiGetClassDevsW             func(*guid, *uint16, uintptr, uint32) uintptr
	setupDiEnumDeviceInterfaces      func(uintptr, unsafe.Pointer, *guid, uint32, *spDeviceInterfaceData) bool
	setupDiGetDeviceInterfaceDetailW func(uintptr, *spDeviceInterfaceData, unsafe.Pointer, uint32, *uint32, unsafe.Pointer) bool
	setupDiDestroyDeviceInfoList     func(uintptr) bool

	createFileW func(*uint16, uint32, uint32, unsafe.Pointer, uint32, uint32, uintptr) uintptr
	closeHandle func(uintptr) bool

	winUSBInitialize             func(uintptr, *uintptr) bool
	winUSBFree                   func(uintptr) bool
	winUSBQueryInterfaceSettings func(uintptr, byte, *usbInterfaceDescriptor) bool
	winUSBQueryPipe              func(uintptr, byte, byte, *winusbPipeInformation) bool
	winUSBGetDescriptor          func(uintptr, uint8, uint8, uint16, unsafe.Pointer, uint32, *uint32) bool
	winUSBReadPipe               func(uintptr, byte, unsafe.Pointer, uint32, *uint32, unsafe.Pointer) bool
	winUSBWritePipe              func(uintptr, byte, unsafe.Pointer, uint32, *uint32, unsafe.Pointer) bool
	winUSBResetPipe              func(uintptr, byte) bool
	winUSBFlushPipe              func(uintptr, byte) bool

	mu      sync.Mutex
	next    handle
	handles map[handle]openedDevice
}

func defaultAPI() API {
	return &windowsAPI{handles: make(map[handle]openedDevice)}
}

func (a *windowsAPI) load() error {
	a.once.Do(func() {
		setupapi, err := syscall.LoadLibrary("setupapi.dll")
		if err != nil {
			a.err = err
			return
		}
		kernel32, err := syscall.LoadLibrary("kernel32.dll")
		if err != nil {
			a.err = err
			return
		}
		winusb, err := syscall.LoadLibrary("winusb.dll")
		if err != nil {
			a.err = err
			return
		}

		purego.RegisterLibFunc(&a.setupDiGetClassDevsW, uintptr(setupapi), "SetupDiGetClassDevsW")
		purego.RegisterLibFunc(&a.setupDiEnumDeviceInterfaces, uintptr(setupapi), "SetupDiEnumDeviceInterfaces")
		purego.RegisterLibFunc(&a.setupDiGetDeviceInterfaceDetailW, uintptr(setupapi), "SetupDiGetDeviceInterfaceDetailW")
		purego.RegisterLibFunc(&a.setupDiDestroyDeviceInfoList, uintptr(setupapi), "SetupDiDestroyDeviceInfoList")
		purego.RegisterLibFunc(&a.createFileW, uintptr(kernel32), "CreateFileW")
		purego.RegisterLibFunc(&a.closeHandle, uintptr(kernel32), "CloseHandle")
		purego.RegisterLibFunc(&a.winUSBInitialize, uintptr(winusb), "WinUsb_Initialize")
		purego.RegisterLibFunc(&a.winUSBFree, uintptr(winusb), "WinUsb_Free")
		purego.RegisterLibFunc(&a.winUSBQueryInterfaceSettings, uintptr(winusb), "WinUsb_QueryInterfaceSettings")
		purego.RegisterLibFunc(&a.winUSBQueryPipe, uintptr(winusb), "WinUsb_QueryPipe")
		purego.RegisterLibFunc(&a.winUSBGetDescriptor, uintptr(winusb), "WinUsb_GetDescriptor")
		purego.RegisterLibFunc(&a.winUSBReadPipe, uintptr(winusb), "WinUsb_ReadPipe")
		purego.RegisterLibFunc(&a.winUSBWritePipe, uintptr(winusb), "WinUsb_WritePipe")
		purego.RegisterLibFunc(&a.winUSBResetPipe, uintptr(winusb), "WinUsb_ResetPipe")
		purego.RegisterLibFunc(&a.winUSBFlushPipe, uintptr(winusb), "WinUsb_FlushPipe")
	})
	return a.err
}

func (a *windowsAPI) DevicePaths() ([]string, error) {
	seen := make(map[string]bool)
	var paths []string
	for _, classID := range deviceInterfaceClasses() {
		classPaths, err := a.devicePathsForClass(classID)
		if err != nil {
			return nil, err
		}
		for _, path := range classPaths {
			if seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (a *windowsAPI) devicePathsForClass(classID guid) ([]string, error) {
	if err := a.load(); err != nil {
		return nil, err
	}

	infoSet := a.setupDiGetClassDevsW(&classID, nil, 0, digcfPresent|digcfDeviceInterface)
	if infoSet == invalidHandleValue {
		return nil, fmt.Errorf("SetupDiGetClassDevsW: %w", lastError())
	}
	defer a.setupDiDestroyDeviceInfoList(infoSet)

	var paths []string
	for index := uint32(0); ; index++ {
		ifaceData := spDeviceInterfaceData{CBSize: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		if !a.setupDiEnumDeviceInterfaces(infoSet, nil, &classID, index, &ifaceData) {
			err := syscall.GetLastError()
			if err == nil || err == syscall.Errno(259) {
				return paths, nil
			}
			return nil, fmt.Errorf("SetupDiEnumDeviceInterfaces: %w", err)
		}

		var required uint32
		_ = a.setupDiGetDeviceInterfaceDetailW(infoSet, &ifaceData, nil, 0, &required, nil)
		if required == 0 {
			return nil, fmt.Errorf("SetupDiGetDeviceInterfaceDetailW size: %w", lastError())
		}

		buf := make([]byte, required)
		binary.LittleEndian.PutUint32(buf[:4], deviceInterfaceDetailDataCBSize())
		if !a.setupDiGetDeviceInterfaceDetailW(infoSet, &ifaceData, unsafe.Pointer(&buf[0]), required, &required, nil) {
			return nil, fmt.Errorf("SetupDiGetDeviceInterfaceDetailW: %w", lastError())
		}
		paths = append(paths, utf16PtrToString((*uint16)(unsafe.Pointer(&buf[4]))))
	}
}

func (a *windowsAPI) Open(path string) (handle, error) {
	if err := a.load(); err != nil {
		return 0, err
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	file := a.createFileW(pathPtr, genericRead|genericWrite, fileShareRead|fileShareWrite, nil, openExisting, fileFlagOverlapped, 0)
	if file == invalidHandleValue {
		return 0, fmt.Errorf("CreateFileW %q: %w", path, lastError())
	}
	var usb uintptr
	if !a.winUSBInitialize(file, &usb) {
		err := lastError()
		_ = a.closeHandle(file)
		return 0, fmt.Errorf("WinUsb_Initialize %q: %w", path, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handles == nil {
		a.handles = make(map[handle]openedDevice)
	}
	a.next++
	h := a.next
	a.handles[h] = openedDevice{file: file, usb: usb, path: path}
	return h, nil
}

func (a *windowsAPI) InterfaceDescriptor(h handle) (InterfaceDescriptor, error) {
	device, err := a.opened(h)
	if err != nil {
		return InterfaceDescriptor{}, err
	}
	var desc usbInterfaceDescriptor
	if !a.winUSBQueryInterfaceSettings(device.usb, 0, &desc) {
		return InterfaceDescriptor{}, fmt.Errorf("WinUsb_QueryInterfaceSettings: %w", lastError())
	}
	return InterfaceDescriptor{
		Class:        desc.InterfaceClass,
		SubClass:     desc.InterfaceSubClass,
		Protocol:     desc.InterfaceProtocol,
		NumEndpoints: desc.NumEndpoints,
	}, nil
}

func (a *windowsAPI) PipeInfos(h handle) ([]PipeInfo, error) {
	device, err := a.opened(h)
	if err != nil {
		return nil, err
	}
	desc, err := a.InterfaceDescriptor(h)
	if err != nil {
		return nil, err
	}
	var pipes []PipeInfo
	for index := byte(0); index < desc.NumEndpoints; index++ {
		var native winusbPipeInformation
		if !a.winUSBQueryPipe(device.usb, 0, index, &native) {
			return nil, fmt.Errorf("WinUsb_QueryPipe(%d): %w", index, lastError())
		}
		pipes = append(pipes, PipeInfo{
			PipeType:      PipeType(native.PipeType),
			PipeID:        native.PipeID,
			MaxPacketSize: native.MaximumPacketSize,
		})
	}
	return pipes, nil
}

func (a *windowsAPI) SerialNumber(h handle) (string, error) {
	device, err := a.opened(h)
	if err != nil {
		return "", err
	}

	var deviceDesc usbDeviceDescriptor
	var transferred uint32
	if !a.winUSBGetDescriptor(device.usb, usbDeviceDescriptorType, 0, 0, unsafe.Pointer(&deviceDesc), uint32(unsafe.Sizeof(deviceDesc)), &transferred) {
		return "", fmt.Errorf("WinUsb_GetDescriptor device: %w", lastError())
	}
	if deviceDesc.SerialNumberIndex == 0 {
		return "", fmt.Errorf("device has no serial string descriptor")
	}

	languageID := defaultLanguageID
	var langBuf [256]byte
	transferred = 0
	if a.winUSBGetDescriptor(device.usb, usbStringDescriptorType, 0, 0, unsafe.Pointer(&langBuf[0]), uint32(len(langBuf)), &transferred) {
		if transferred >= 4 && langBuf[1] == usbStringDescriptorType {
			languageID = binary.LittleEndian.Uint16(langBuf[2:4])
		}
	}

	var serialBuf [512]byte
	transferred = 0
	if !a.winUSBGetDescriptor(device.usb, usbStringDescriptorType, deviceDesc.SerialNumberIndex, languageID, unsafe.Pointer(&serialBuf[0]), uint32(len(serialBuf)), &transferred) {
		return "", fmt.Errorf("WinUsb_GetDescriptor serial: %w", lastError())
	}
	serial, ok := DecodeUSBStringDescriptor(serialBuf[:transferred])
	if !ok || serial == "" {
		return "", fmt.Errorf("invalid USB serial string descriptor")
	}
	return serial, nil
}

func (a *windowsAPI) Close(h handle) error {
	if err := a.load(); err != nil {
		return err
	}

	a.mu.Lock()
	device, ok := a.handles[h]
	if ok {
		delete(a.handles, h)
	}
	a.mu.Unlock()
	if !ok {
		return nil
	}

	var firstErr error
	if device.usb != 0 && !a.winUSBFree(device.usb) {
		firstErr = fmt.Errorf("WinUsb_Free: %w", lastError())
	}
	if device.file != 0 && !a.closeHandle(device.file) && firstErr == nil {
		firstErr = fmt.Errorf("CloseHandle: %w", lastError())
	}
	return firstErr
}

func (a *windowsAPI) ReadPipe(h handle, pipeID byte, p []byte) (int, error) {
	device, err := a.opened(h)
	if err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	var read uint32
	if !a.winUSBReadPipe(device.usb, pipeID, unsafe.Pointer(&p[0]), uint32(len(p)), &read, nil) {
		return int(read), fmt.Errorf("WinUsb_ReadPipe(%#x): %w", pipeID, lastError())
	}
	return int(read), nil
}

func (a *windowsAPI) WritePipe(h handle, pipeID byte, p []byte) (int, error) {
	device, err := a.opened(h)
	if err != nil {
		return 0, err
	}
	var ptr unsafe.Pointer
	if len(p) > 0 {
		ptr = unsafe.Pointer(&p[0])
	}
	var written uint32
	if !a.winUSBWritePipe(device.usb, pipeID, ptr, uint32(len(p)), &written, nil) {
		return int(written), fmt.Errorf("WinUsb_WritePipe(%#x): %w", pipeID, lastError())
	}
	return int(written), nil
}

func (a *windowsAPI) ResetPipe(h handle, pipeID byte) error {
	device, err := a.opened(h)
	if err != nil {
		return err
	}
	if !a.winUSBResetPipe(device.usb, pipeID) {
		return fmt.Errorf("WinUsb_ResetPipe(%#x): %w", pipeID, lastError())
	}
	return nil
}

func (a *windowsAPI) FlushPipe(h handle, pipeID byte) error {
	device, err := a.opened(h)
	if err != nil {
		return err
	}
	if !a.winUSBFlushPipe(device.usb, pipeID) {
		return fmt.Errorf("WinUsb_FlushPipe(%#x): %w", pipeID, lastError())
	}
	return nil
}

func (a *windowsAPI) opened(h handle) (openedDevice, error) {
	if err := a.load(); err != nil {
		return openedDevice{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	device, ok := a.handles[h]
	if !ok {
		return openedDevice{}, fmt.Errorf("unknown WinUSB handle %d", h)
	}
	return device, nil
}

func deviceInterfaceDetailDataCBSize() uint32 {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return 6
	}
	return 8
}

func utf16PtrToString(ptr *uint16) string {
	if ptr == nil {
		return ""
	}
	var values []uint16
	for offset := 0; ; offset++ {
		value := *(*uint16)(unsafe.Add(unsafe.Pointer(ptr), offset*2))
		if value == 0 {
			break
		}
		values = append(values, value)
	}
	return syscall.UTF16ToString(values)
}

func lastError() error {
	err := syscall.GetLastError()
	if err == nil || err == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return err
}
