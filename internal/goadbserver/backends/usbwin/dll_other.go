//go:build !windows

package usbwin

type unsupportedAPI struct{}

func defaultAPI() API {
	return unsupportedAPI{}
}

func (unsupportedAPI) EnumInterfaces() (handle, error) { return 0, errUnsupported }
func (unsupportedAPI) NextInterface(handle) (string, bool, error) {
	return "", false, errUnsupported
}
func (unsupportedAPI) OpenInterface(string) (handle, error) { return 0, errUnsupported }
func (unsupportedAPI) InterfaceDescriptor(handle) (InterfaceDescriptor, error) {
	return InterfaceDescriptor{}, errUnsupported
}
func (unsupportedAPI) SerialNumber(handle) (string, error)       { return "", errUnsupported }
func (unsupportedAPI) OpenReadEndpoint(handle) (handle, error)   { return 0, errUnsupported }
func (unsupportedAPI) OpenWriteEndpoint(handle) (handle, error)  { return 0, errUnsupported }
func (unsupportedAPI) Close(handle) error                        { return nil }
func (unsupportedAPI) ReadEndpoint(handle, []byte) (int, error)  { return 0, errUnsupported }
func (unsupportedAPI) WriteEndpoint(handle, []byte) (int, error) { return 0, errUnsupported }
