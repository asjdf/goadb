package usbwin

import (
	"fmt"
	"io"
)

const (
	ADBClass    = 0xff
	ADBSubClass = 0x42
	ADBProtocol = 0x01
)

type handle uintptr

type InterfaceDescriptor struct {
	Class    uint8
	SubClass uint8
	Protocol uint8
}

type API interface {
	EnumInterfaces() (handle, error)
	NextInterface(enum handle) (name string, ok bool, err error)
	OpenInterface(name string) (handle, error)
	InterfaceDescriptor(iface handle) (InterfaceDescriptor, error)
	SerialNumber(iface handle) (string, error)
	OpenReadEndpoint(iface handle) (handle, error)
	OpenWriteEndpoint(iface handle) (handle, error)
	Close(h handle) error
	ReadEndpoint(h handle, p []byte) (int, error)
	WriteEndpoint(h handle, p []byte) (int, error)
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
	enum, err := b.api.EnumInterfaces()
	if err != nil {
		return nil, err
	}
	defer b.api.Close(enum)

	var devices []Device
	for {
		name, ok, err := b.api.NextInterface(enum)
		if err != nil {
			return nil, err
		}
		if !ok {
			return devices, nil
		}

		iface, err := b.api.OpenInterface(name)
		if err != nil {
			continue
		}
		desc, err := b.api.InterfaceDescriptor(iface)
		if err != nil {
			_ = b.api.Close(iface)
			return nil, err
		}
		if !IsADBInterface(desc) {
			_ = b.api.Close(iface)
			continue
		}

		serial, err := b.api.SerialNumber(iface)
		if err != nil {
			_ = b.api.Close(iface)
			return nil, err
		}
		readEndpoint, err := b.api.OpenReadEndpoint(iface)
		if err != nil {
			_ = b.api.Close(iface)
			return nil, err
		}
		writeEndpoint, err := b.api.OpenWriteEndpoint(iface)
		if err != nil {
			_ = b.api.Close(readEndpoint)
			_ = b.api.Close(iface)
			return nil, err
		}

		devices = append(devices, Device{
			Name:   name,
			Serial: serial,
			Conn:   newEndpointConn(b.api, iface, readEndpoint, writeEndpoint),
		})
	}
}

func IsADBInterface(desc InterfaceDescriptor) bool {
	return desc.Class == ADBClass && desc.SubClass == ADBSubClass && desc.Protocol == ADBProtocol
}

type endpointAPI interface {
	Close(h handle) error
	ReadEndpoint(h handle, p []byte) (int, error)
	WriteEndpoint(h handle, p []byte) (int, error)
}

type endpointConn struct {
	api           endpointAPI
	iface         handle
	readEndpoint  handle
	writeEndpoint handle
	closed        bool
}

func newEndpointConn(api endpointAPI, iface, readEndpoint, writeEndpoint handle) *endpointConn {
	return &endpointConn{api: api, iface: iface, readEndpoint: readEndpoint, writeEndpoint: writeEndpoint}
}

func (c *endpointConn) Read(p []byte) (int, error) {
	if c.closed {
		return 0, fmt.Errorf("usb endpoint connection closed")
	}
	return c.api.ReadEndpoint(c.readEndpoint, p)
}

func (c *endpointConn) Write(p []byte) (int, error) {
	if c.closed {
		return 0, fmt.Errorf("usb endpoint connection closed")
	}
	return c.api.WriteEndpoint(c.writeEndpoint, p)
}

func (c *endpointConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	var firstErr error
	for _, h := range []handle{c.readEndpoint, c.writeEndpoint, c.iface} {
		if err := c.api.Close(h); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
