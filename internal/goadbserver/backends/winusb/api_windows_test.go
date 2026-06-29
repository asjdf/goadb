//go:build windows

package winusb

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestDefaultAPIUsesWindowsDLLImplementation(t *testing.T) {
	if _, ok := defaultAPI().(*windowsAPI); !ok {
		t.Fatalf("defaultAPI() = %T, want *windowsAPI", defaultAPI())
	}
}

func TestWindowsStructureLayout(t *testing.T) {
	wantDetailSize := uint32(8)
	if runtime.GOARCH == "386" {
		wantDetailSize = 6
	}
	if got := deviceInterfaceDetailDataCBSize(); got != wantDetailSize {
		t.Fatalf("deviceInterfaceDetailDataCBSize = %d, want %d", got, wantDetailSize)
	}
	if got := unsafe.Offsetof(spDeviceInterfaceDetailData{}.DevicePath); got != 4 {
		t.Fatalf("SP_DEVICE_INTERFACE_DETAIL_DATA_W DevicePath offset = %d, want 4", got)
	}
	if got := unsafe.Sizeof(winusbPipeInformation{}); got != 12 {
		t.Fatalf("WINUSB_PIPE_INFORMATION size = %d, want 12", got)
	}
	if got := unsafe.Offsetof(winusbPipeInformation{}.MaximumPacketSize); got != 6 {
		t.Fatalf("WINUSB_PIPE_INFORMATION MaximumPacketSize offset = %d, want 6", got)
	}
}

func TestWindowsAPICloseIgnoresUnknownHandle(t *testing.T) {
	api := defaultAPI().(*windowsAPI)
	if err := api.Close(4242); err != nil {
		t.Fatalf("Close unknown handle returned error: %v", err)
	}
}

func TestDeviceInterfaceClassesIncludeAndroidAndGenericUSB(t *testing.T) {
	classes := deviceInterfaceClasses()
	if len(classes) != 2 {
		t.Fatalf("deviceInterfaceClasses returned %d classes, want 2", len(classes))
	}
	if classes[0] != androidUSBClassID {
		t.Fatalf("first class = %#v, want androidUSBClassID", classes[0])
	}
	if classes[1] != usbDeviceInterfaceGUID {
		t.Fatalf("second class = %#v, want usbDeviceInterfaceGUID", classes[1])
	}
}
