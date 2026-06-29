package smartsocket

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadProtocolStringReadsHexLengthPrefixedPayload(t *testing.T) {
	got, err := ReadProtocolString(strings.NewReader("000Chost:version"))
	if err != nil {
		t.Fatalf("ReadProtocolString returned error: %v", err)
	}
	if got != "host:version" {
		t.Fatalf("ReadProtocolString() = %q, want %q", got, "host:version")
	}
}

func TestReadProtocolStringRejectsInvalidHexLength(t *testing.T) {
	_, err := ReadProtocolString(strings.NewReader("zzzzhost:version"))
	if err == nil {
		t.Fatal("ReadProtocolString succeeded, want invalid length error")
	}
}

func TestReadProtocolStringRejectsShortPayload(t *testing.T) {
	_, err := ReadProtocolString(strings.NewReader("000Chost"))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadProtocolString error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestWriteProtocolStringWritesLowercaseHexLengthPrefix(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteProtocolString(&buf, "host:devices"); err != nil {
		t.Fatalf("WriteProtocolString returned error: %v", err)
	}
	if got, want := buf.String(), "000chost:devices"; got != want {
		t.Fatalf("WriteProtocolString wrote %q, want %q", got, want)
	}
}

func TestWriteProtocolStringRejectsOversizedPayload(t *testing.T) {
	err := WriteProtocolString(io.Discard, strings.Repeat("x", MaxProtocolStringLength+1))
	if err == nil {
		t.Fatal("WriteProtocolString succeeded, want oversized payload error")
	}
}

func TestWriteStatusHelpers(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOKAY(&buf); err != nil {
		t.Fatalf("WriteOKAY returned error: %v", err)
	}
	if err := WriteFAIL(&buf, "no transport"); err != nil {
		t.Fatalf("WriteFAIL returned error: %v", err)
	}
	if got, want := buf.String(), "OKAYFAIL000cno transport"; got != want {
		t.Fatalf("status helpers wrote %q, want %q", got, want)
	}
}
