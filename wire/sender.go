package wire

import (
	"fmt"
	"io"

	"github.com/asjdf/goadb/internal/errors"
)

const MaxPayload = 4096

// Sender sends messages to the server.
type Sender interface {
	SendMessage(msg []byte) error

	NewSyncSender() SyncSender

	Close() error

	io.Writer
}

type realSender struct {
	io.WriteCloser
}

func NewSender(w io.WriteCloser) Sender {
	return &realSender{w}
}

func SendMessageString(s Sender, msg string) error {
	return s.SendMessage([]byte(msg))
}

func (s *realSender) SendMessage(msg []byte) error {
	if len(msg) > MaxPayload {
		return errors.AssertionErrorf("message length exceeds maximum: %d", len(msg))
	}

	return SendProtocolString(s.WriteCloser, string(msg))
}

func (s *realSender) NewSyncSender() SyncSender {
	return NewSyncSender(s.WriteCloser)
}

func (s *realSender) Close() error {
	return errors.WrapErrorf(s.WriteCloser.Close(), errors.NetworkError, "error closing sender")
}

func (s *realSender) Writer() io.Writer {
	return s.WriteCloser
}

var _ Sender = &realSender{}

func SendProtocolString(w io.Writer, msg string) error {
	length := len(msg)
	if length > MaxPayload-4 {
		return fmt.Errorf("protocol string too long: %d", length)
	}

	// 格式化字符串并发送
	formattedString := fmt.Sprintf("%04x%s", length, msg)
	_, err := w.Write([]byte(formattedString))
	if err != nil {
		return errors.WrapErrorf(err, errors.NetworkError, "error sending protocol string")
	}
	return nil
}
