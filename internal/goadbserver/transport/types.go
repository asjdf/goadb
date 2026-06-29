package transport

import "errors"

type Type string

const (
	TypeUSB Type = "usb"
	TypeTCP Type = "tcp"
)

type State string

const (
	StateConnecting   State = "connecting"
	StateAuthorizing  State = "authorizing"
	StateUnauthorized State = "unauthorized"
	StateOffline      State = "offline"
	StateDevice       State = "device"
)

type Info struct {
	ID       uint64
	Serial   string
	Type     Type
	State    State
	DevPath  string
	Product  string
	Model    string
	Device   string
	Features []string
}

type SelectorKind int

const (
	SelectorAny SelectorKind = iota
	SelectorUSB
	SelectorLocal
	SelectorSerial
)

type Selector struct {
	Kind   SelectorKind
	Serial string
}

var (
	ErrTransportNotFound  = errors.New("transport not found")
	ErrAmbiguousTransport = errors.New("more than one transport matched")
)

func AnySelector() Selector {
	return Selector{Kind: SelectorAny}
}

func USBSelector() Selector {
	return Selector{Kind: SelectorUSB}
}

func LocalSelector() Selector {
	return Selector{Kind: SelectorLocal}
}

func SerialSelector(serial string) Selector {
	return Selector{Kind: SelectorSerial, Serial: serial}
}
