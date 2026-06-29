//go:build !windows

package winusb

import "fmt"

func defaultAPI() API {
	return unsupportedAPI{}
}

type unsupportedAPI struct{}

func (unsupportedAPI) DevicePaths() ([]string, error) {
	return nil, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) Open(path string) (handle, error) {
	return 0, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) InterfaceDescriptor(h handle) (InterfaceDescriptor, error) {
	return InterfaceDescriptor{}, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) PipeInfos(h handle) ([]PipeInfo, error) {
	return nil, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) SerialNumber(h handle) (string, error) {
	return "", fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) Close(h handle) error {
	return nil
}

func (unsupportedAPI) ReadPipe(h handle, pipeID byte, p []byte) (int, error) {
	return 0, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) WritePipe(h handle, pipeID byte, p []byte) (int, error) {
	return 0, fmt.Errorf("direct WinUSB API is only supported on windows")
}

func (unsupportedAPI) ResetPipe(h handle, pipeID byte) error {
	return nil
}

func (unsupportedAPI) FlushPipe(h handle, pipeID byte) error {
	return nil
}
