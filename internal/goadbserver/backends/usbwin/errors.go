package usbwin

import "errors"

var errUnsupported = errors.New("windows adb usb backend is only supported on windows")
