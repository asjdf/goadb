package adbproto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestCommandConstantsUseADBWireValues(t *testing.T) {
	tests := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"SYNC", CmdSYNC, 0x434e5953},
		{"CNXN", CmdCNXN, 0x4e584e43},
		{"AUTH", CmdAUTH, 0x48545541},
		{"OPEN", CmdOPEN, 0x4e45504f},
		{"OKAY", CmdOKAY, 0x59414b4f},
		{"CLSE", CmdCLSE, 0x45534c43},
		{"WRTE", CmdWRTE, 0x45545257},
		{"STLS", CmdSTLS, 0x534c5453},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %#x, want %#x", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestCalculateChecksumSumsPayloadBytes(t *testing.T) {
	if got, want := CalculateChecksum([]byte{0, 1, 2, 255}), uint32(258); got != want {
		t.Fatalf("CalculateChecksum() = %d, want %d", got, want)
	}
}

func TestWritePacketWritesLittleEndianHeaderAndPayload(t *testing.T) {
	var buf bytes.Buffer
	packet := Packet{
		Command: CmdOPEN,
		Arg0:    7,
		Arg1:    9,
		Payload: []byte("shell:id"),
	}
	if err := WritePacket(&buf, packet); err != nil {
		t.Fatalf("WritePacket returned error: %v", err)
	}

	raw := buf.Bytes()
	if got, want := len(raw), HeaderLength+len(packet.Payload); got != want {
		t.Fatalf("packet length = %d, want %d", got, want)
	}
	fields := []uint32{
		binary.LittleEndian.Uint32(raw[0:4]),
		binary.LittleEndian.Uint32(raw[4:8]),
		binary.LittleEndian.Uint32(raw[8:12]),
		binary.LittleEndian.Uint32(raw[12:16]),
		binary.LittleEndian.Uint32(raw[16:20]),
		binary.LittleEndian.Uint32(raw[20:24]),
	}
	wantFields := []uint32{
		CmdOPEN,
		7,
		9,
		uint32(len(packet.Payload)),
		CalculateChecksum(packet.Payload),
		CmdOPEN ^ 0xffffffff,
	}
	for i := range fields {
		if fields[i] != wantFields[i] {
			t.Fatalf("field %d = %#x, want %#x", i, fields[i], wantFields[i])
		}
	}
	if got := string(raw[HeaderLength:]); got != "shell:id" {
		t.Fatalf("payload = %q, want shell:id", got)
	}
}

func TestReadPacketReadsWrittenPacket(t *testing.T) {
	var buf bytes.Buffer
	want := Packet{Command: CmdWRTE, Arg0: 2, Arg1: 1, Payload: []byte("hello")}
	if err := WritePacket(&buf, want); err != nil {
		t.Fatalf("WritePacket returned error: %v", err)
	}

	got, err := ReadPacket(&buf, MaxPayload)
	if err != nil {
		t.Fatalf("ReadPacket returned error: %v", err)
	}
	if got.Command != want.Command || got.Arg0 != want.Arg0 || got.Arg1 != want.Arg1 || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("ReadPacket() = %#v, want %#v", got, want)
	}
}

func TestReadPacketRejectsBadMagic(t *testing.T) {
	var raw bytes.Buffer
	writeHeaderForTest(t, &raw, CmdWRTE, 1, 2, []byte("x"), 0)
	_, err := ReadPacket(&raw, MaxPayload)
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("ReadPacket error = %v, want ErrInvalidMagic", err)
	}
}

func TestReadPacketRejectsOversizedPayload(t *testing.T) {
	var raw bytes.Buffer
	payload := []byte("abc")
	writeHeaderForTest(t, &raw, CmdWRTE, 1, 2, payload, CmdWRTE^0xffffffff)
	data := raw.Bytes()
	binary.LittleEndian.PutUint32(data[12:16], MaxPayload+1)

	_, err := ReadPacket(bytes.NewReader(data), MaxPayload)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("ReadPacket error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestReadPacketReturnsUnexpectedEOFForShortHeader(t *testing.T) {
	_, err := ReadPacket(bytes.NewReader([]byte{1, 2, 3}), MaxPayload)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadPacket error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func writeHeaderForTest(t *testing.T, w io.Writer, command, arg0, arg1 uint32, payload []byte, magic uint32) {
	t.Helper()
	fields := []uint32{
		command,
		arg0,
		arg1,
		uint32(len(payload)),
		CalculateChecksum(payload),
		magic,
	}
	for _, field := range fields {
		if err := binary.Write(w, binary.LittleEndian, field); err != nil {
			t.Fatalf("binary.Write returned error: %v", err)
		}
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("payload write returned error: %v", err)
	}
}
