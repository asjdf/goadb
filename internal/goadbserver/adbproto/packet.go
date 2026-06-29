package adbproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	CmdSYNC uint32 = 0x434e5953
	CmdCNXN uint32 = 0x4e584e43
	CmdOPEN uint32 = 0x4e45504f
	CmdOKAY uint32 = 0x59414b4f
	CmdCLSE uint32 = 0x45534c43
	CmdWRTE uint32 = 0x45545257
	CmdAUTH uint32 = 0x48545541
	CmdSTLS uint32 = 0x534c5453

	Version       uint32 = 0x01000001
	VersionMin    uint32 = 0x01000000
	MaxPayload    uint32 = 1024 * 1024
	HeaderLength         = 24
	AuthToken     uint32 = 1
	AuthSignature uint32 = 2
	AuthPublicKey uint32 = 3
)

var (
	ErrInvalidMagic    = errors.New("invalid adb packet magic")
	ErrPayloadTooLarge = errors.New("adb packet payload too large")
)

type Packet struct {
	Command uint32
	Arg0    uint32
	Arg1    uint32
	Payload []byte
}

func CalculateChecksum(payload []byte) uint32 {
	var checksum uint32
	for _, b := range payload {
		checksum += uint32(b)
	}
	return checksum
}

func WritePacket(w io.Writer, packet Packet) error {
	if len(packet.Payload) > int(MaxPayload) {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(packet.Payload), MaxPayload)
	}

	fields := [6]uint32{
		packet.Command,
		packet.Arg0,
		packet.Arg1,
		uint32(len(packet.Payload)),
		CalculateChecksum(packet.Payload),
		packet.Command ^ 0xffffffff,
	}
	var header [HeaderLength]byte
	for i, field := range fields {
		binary.LittleEndian.PutUint32(header[i*4:(i+1)*4], field)
	}

	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(packet.Payload) == 0 {
		return nil
	}
	_, err := w.Write(packet.Payload)
	return err
}

func ReadPacket(r io.Reader, maxPayload uint32) (Packet, error) {
	var header [HeaderLength]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Packet{}, err
	}

	packet := Packet{
		Command: binary.LittleEndian.Uint32(header[0:4]),
		Arg0:    binary.LittleEndian.Uint32(header[4:8]),
		Arg1:    binary.LittleEndian.Uint32(header[8:12]),
	}
	length := binary.LittleEndian.Uint32(header[12:16])
	magic := binary.LittleEndian.Uint32(header[20:24])

	if magic != packet.Command^0xffffffff {
		return Packet{}, fmt.Errorf("%w: command=%#x magic=%#x", ErrInvalidMagic, packet.Command, magic)
	}
	if length > maxPayload {
		return Packet{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, length, maxPayload)
	}

	packet.Payload = make([]byte, int(length))
	if _, err := io.ReadFull(r, packet.Payload); err != nil {
		return Packet{}, err
	}
	return packet, nil
}
