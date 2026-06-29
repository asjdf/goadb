package winusb

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

const (
	ADBClass    = 0xff
	ADBSubClass = 0x42
	ADBProtocol = 0x01
)

type handle uintptr

type PipeType uint32

const (
	PipeTypeControl     PipeType = 0
	PipeTypeIsochronous PipeType = 1
	PipeTypeBulk        PipeType = 2
	PipeTypeInterrupt   PipeType = 3
)

type InterfaceDescriptor struct {
	Class        uint8
	SubClass     uint8
	Protocol     uint8
	NumEndpoints uint8
}

type PipeInfo struct {
	PipeType      PipeType
	PipeID        byte
	MaxPacketSize uint16
}

type API interface {
	DevicePaths() ([]string, error)
	Open(path string) (handle, error)
	InterfaceDescriptor(h handle) (InterfaceDescriptor, error)
	PipeInfos(h handle) ([]PipeInfo, error)
	SerialNumber(h handle) (string, error)
	Close(h handle) error
	ReadPipe(h handle, pipeID byte, p []byte) (int, error)
	WritePipe(h handle, pipeID byte, p []byte) (int, error)
	ResetPipe(h handle, pipeID byte) error
	FlushPipe(h handle, pipeID byte) error
}

type Options struct {
	API API
}

type Backend struct {
	api API
}

type Device struct {
	Name   string
	Serial string
	Conn   io.ReadWriteCloser
}

func New(opts Options) *Backend {
	api := opts.API
	if api == nil {
		api = defaultAPI()
	}
	return &Backend{api: api}
}

func (b *Backend) Enumerate() ([]Device, error) {
	paths, err := b.api.DevicePaths()
	if err != nil {
		return nil, err
	}

	var devices []Device
	for _, path := range paths {
		h, err := b.api.Open(path)
		if err != nil {
			continue
		}

		desc, err := b.api.InterfaceDescriptor(h)
		if err != nil {
			_ = b.api.Close(h)
			return nil, err
		}
		if !IsADBInterface(desc) {
			_ = b.api.Close(h)
			continue
		}

		readPipe, writePipe, maxPacketSize, err := selectBulkPipes(b.api, h)
		if err != nil {
			_ = b.api.Close(h)
			return nil, err
		}
		if err := preparePipes(b.api, h, readPipe, writePipe); err != nil {
			_ = b.api.Close(h)
			return nil, err
		}

		serial, err := b.api.SerialNumber(h)
		if err != nil || serial == "" {
			serial = SerialFromDevicePath(path)
		}
		if serial == "" {
			_ = b.api.Close(h)
			return nil, fmt.Errorf("usb serial unavailable for %q", path)
		}

		devices = append(devices, Device{
			Name:   path,
			Serial: serial,
			Conn:   newEndpointConn(b.api, h, readPipe, writePipe, maxPacketSize),
		})
	}
	return devices, nil
}

func IsADBInterface(desc InterfaceDescriptor) bool {
	return desc.Class == ADBClass && desc.SubClass == ADBSubClass && desc.Protocol == ADBProtocol
}

func selectBulkPipes(api API, h handle) (readPipe, writePipe byte, maxPacketSize uint16, err error) {
	pipes, err := api.PipeInfos(h)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, pipe := range pipes {
		if pipe.PipeType != PipeTypeBulk {
			continue
		}
		if pipe.PipeID&0x80 != 0 && readPipe == 0 {
			readPipe = pipe.PipeID
		}
		if pipe.PipeID&0x80 == 0 && writePipe == 0 {
			writePipe = pipe.PipeID
			maxPacketSize = pipe.MaxPacketSize
		}
	}
	if readPipe == 0 || writePipe == 0 {
		return 0, 0, 0, fmt.Errorf("adb bulk endpoints not found")
	}
	return readPipe, writePipe, maxPacketSize, nil
}

func preparePipes(api API, h handle, readPipe, writePipe byte) error {
	if err := api.ResetPipe(h, readPipe); err != nil {
		return err
	}
	if err := api.ResetPipe(h, writePipe); err != nil {
		return err
	}
	return api.FlushPipe(h, readPipe)
}

func SerialFromDevicePath(path string) string {
	parts := strings.Split(path, "#")
	if len(parts) < 4 {
		return ""
	}
	last := parts[len(parts)-1]
	if !strings.HasPrefix(last, "{") {
		return ""
	}
	serialParts := parts[2 : len(parts)-1]
	if len(serialParts) == 0 {
		return ""
	}
	return strings.Join(serialParts, "/")
}

func DecodeUSBStringDescriptor(buf []byte) (string, bool) {
	if len(buf) < 2 || buf[1] != 3 {
		return "", false
	}
	length := int(buf[0])
	if length < 2 || length > len(buf) || length%2 != 0 {
		return "", false
	}
	words := make([]uint16, 0, (length-2)/2)
	for i := 2; i < length; i += 2 {
		words = append(words, uint16(buf[i])|uint16(buf[i+1])<<8)
	}
	return string(utf16.Decode(words)), true
}

type endpointConn struct {
	api           API
	h             handle
	readPipe      byte
	writePipe     byte
	maxPacketSize uint16
	closed        bool
}

func newEndpointConn(api API, h handle, readPipe, writePipe byte, maxPacketSize uint16) *endpointConn {
	return &endpointConn{api: api, h: h, readPipe: readPipe, writePipe: writePipe, maxPacketSize: maxPacketSize}
}

func (c *endpointConn) Read(p []byte) (int, error) {
	if c.closed {
		return 0, fmt.Errorf("winusb endpoint connection closed")
	}
	n, err := c.api.ReadPipe(c.h, c.readPipe, p)
	if err != nil {
		_ = c.api.ResetPipe(c.h, c.readPipe)
	}
	return n, err
}

func (c *endpointConn) Write(p []byte) (int, error) {
	if c.closed {
		return 0, fmt.Errorf("winusb endpoint connection closed")
	}
	n, err := c.api.WritePipe(c.h, c.writePipe, p)
	if err != nil {
		_ = c.api.ResetPipe(c.h, c.writePipe)
		return n, err
	}
	if n != len(p) {
		_ = c.api.ResetPipe(c.h, c.writePipe)
		return n, io.ErrShortWrite
	}
	if c.maxPacketSize != 0 && len(p) != 0 && len(p)%int(c.maxPacketSize) == 0 {
		if _, err := c.api.WritePipe(c.h, c.writePipe, nil); err != nil {
			_ = c.api.ResetPipe(c.h, c.writePipe)
			return n, err
		}
	}
	return n, nil
}

func (c *endpointConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.api.Close(c.h)
}
