package wire

import (
	"bytes"
	"io"
	"testing"
)

func TestShellConnCloseStdinWritesShellV2CloseStdinPacket(t *testing.T) {
	sender := &recordingShellSender{}
	shell, err := NewShellV2Conn(NewConn(&emptyShellScanner{}, sender))
	if err != nil {
		t.Fatalf("NewShellV2Conn returned error: %v", err)
	}

	if err := shell.CloseStdin(); err != nil {
		t.Fatalf("CloseStdin returned error: %v", err)
	}

	want := []byte{4, 0, 0, 0, 0}
	if !bytes.Equal(sender.writes.Bytes(), want) {
		t.Fatalf("written packet = %v, want %v", sender.writes.Bytes(), want)
	}
}

func TestShellConnSetWindowSizeWritesShellV2WindowSizePacket(t *testing.T) {
	sender := &recordingShellSender{}
	shell, err := NewShellV2Conn(NewConn(&emptyShellScanner{}, sender))
	if err != nil {
		t.Fatalf("NewShellV2Conn returned error: %v", err)
	}

	if err := shell.SetWindowSize(24, 80, 0, 0); err != nil {
		t.Fatalf("SetWindowSize returned error: %v", err)
	}

	want := append([]byte{5, 10, 0, 0, 0}, []byte("24x80,0x0\x00")...)
	if !bytes.Equal(sender.writes.Bytes(), want) {
		t.Fatalf("written packet = %v, want %v", sender.writes.Bytes(), want)
	}
}

type emptyShellScanner struct{}

func (s *emptyShellScanner) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (s *emptyShellScanner) Close() error {
	return nil
}

func (s *emptyShellScanner) ReadStatus(req string) (string, error) {
	return StatusSuccess, nil
}

func (s *emptyShellScanner) ReadMessage() ([]byte, error) {
	return nil, io.EOF
}

func (s *emptyShellScanner) ReadUntilEof() ([]byte, error) {
	return nil, io.EOF
}

func (s *emptyShellScanner) NewSyncScanner() SyncScanner {
	return nil
}

type recordingShellSender struct {
	writes bytes.Buffer
}

func (s *recordingShellSender) SendMessage(msg []byte) error {
	_, err := s.writes.Write(msg)
	return err
}

func (s *recordingShellSender) NewSyncSender() SyncSender {
	return nil
}

func (s *recordingShellSender) Close() error {
	return nil
}

func (s *recordingShellSender) Write(p []byte) (int, error) {
	return s.writes.Write(p)
}
