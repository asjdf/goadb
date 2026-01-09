package adb

import (
	"io"
	"strings"

	"github.com/asjdf/goadb/internal/errors"
	"github.com/asjdf/goadb/wire"
)

// MockServer implements Server, Scanner, and Sender.
type MockServer struct {
	// Each time an operation is performed, if this slice is non-empty, the head element
	// of this slice is returned and removed from the slice. If the head is nil, it is removed
	// but not returned.
	Errs []error

	Status string

	// Messages are returned from read calls in order, each preceded by a length header.
	Messages     []string
	nextMsgIndex int

	// readPos tracks the current read position within the current message for Read() calls.
	// This is separate from nextMsgIndex because Read() supports partial reads.
	readPos      int
	readMsgIndex int

	// Each message passed to a send call is appended to this slice.
	Requests []string

	// Each time an operation is performed, its name is appended to this slice.
	Trace []string
}

func (s *MockServer) Read(p []byte) (n int, err error) {
	s.logMethod("Read")
	if err := s.getNextErrToReturn(); err != nil {
		return 0, err
	}

	if len(p) == 0 {
		return 0, nil
	}

	// 如果没有更多消息可读，返回 EOF
	if s.readMsgIndex >= len(s.Messages) {
		return 0, errors.WrapErrorf(io.EOF, errors.NetworkError, "")
	}

	// 获取当前消息
	msg := []byte(s.Messages[s.readMsgIndex])
	msgLen := len(msg)

	// 如果当前消息已经读取完毕，移动到下一个消息
	if s.readPos >= msgLen {
		s.readMsgIndex++
		s.readPos = 0
		// 检查是否还有更多消息
		if s.readMsgIndex >= len(s.Messages) {
			return 0, errors.WrapErrorf(io.EOF, errors.NetworkError, "")
		}
		msg = []byte(s.Messages[s.readMsgIndex])
		msgLen = len(msg)
	}

	// 计算可以读取的字节数
	available := msgLen - s.readPos
	toRead := len(p)
	if toRead > available {
		toRead = available
	}

	// 复制数据到缓冲区
	copy(p, msg[s.readPos:s.readPos+toRead])
	s.readPos += toRead

	// 如果当前消息读取完毕，移动到下一个消息
	if s.readPos >= msgLen {
		s.readMsgIndex++
		s.readPos = 0
	}

	return toRead, nil
}

func (s *MockServer) Write(p []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

var _ server = &MockServer{}

func (s *MockServer) Dial() (*wire.Conn, error) {
	s.logMethod("Dial")
	if err := s.getNextErrToReturn(); err != nil {
		return nil, err
	}
	return wire.NewConn(s, s), nil
}

func (s *MockServer) Start() error {
	s.logMethod("Start")
	return nil
}

func (s *MockServer) StartDebug() error {
	s.logMethod("StartDebug")
	return nil
}

func (s *MockServer) ReadStatus(req string) (string, error) {
	s.logMethod("ReadStatus")
	if err := s.getNextErrToReturn(); err != nil {
		return "", err
	}
	return s.Status, nil
}

func (s *MockServer) ReadMessage() ([]byte, error) {
	s.logMethod("ReadMessage")
	if err := s.getNextErrToReturn(); err != nil {
		return nil, err
	}
	if s.nextMsgIndex >= len(s.Messages) {
		return nil, errors.WrapErrorf(io.EOF, errors.NetworkError, "")
	}

	s.nextMsgIndex++
	return []byte(s.Messages[s.nextMsgIndex-1]), nil
}

func (s *MockServer) ReadUntilEof() ([]byte, error) {
	s.logMethod("ReadUntilEof")
	if err := s.getNextErrToReturn(); err != nil {
		return nil, err
	}

	var data []string
	for ; s.nextMsgIndex < len(s.Messages); s.nextMsgIndex++ {
		data = append(data, s.Messages[s.nextMsgIndex])
	}
	return []byte(strings.Join(data, "")), nil
}

func (s *MockServer) SendMessage(msg []byte) error {
	s.logMethod("SendMessage")
	if err := s.getNextErrToReturn(); err != nil {
		return err
	}
	s.Requests = append(s.Requests, string(msg))
	return nil
}

func (s *MockServer) NewSyncScanner() wire.SyncScanner {
	s.logMethod("NewSyncScanner")
	return nil
}

func (s *MockServer) NewSyncSender() wire.SyncSender {
	s.logMethod("NewSyncSender")
	return nil
}

func (s *MockServer) Close() error {
	s.logMethod("Close")
	if err := s.getNextErrToReturn(); err != nil {
		return err
	}
	return nil
}

func (s *MockServer) getNextErrToReturn() (err error) {
	if len(s.Errs) > 0 {
		err = s.Errs[0]
		s.Errs = s.Errs[1:]
	}
	return
}

func (s *MockServer) logMethod(name string) {
	s.Trace = append(s.Trace, name)
}
