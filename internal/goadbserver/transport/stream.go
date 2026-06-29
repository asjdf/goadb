package transport

import (
	"errors"
	"io"
	"sync"

	"github.com/asjdf/goadb/internal/goadbserver/adbproto"
)

var ErrStreamClosed = errors.New("adb stream closed")

type Stream struct {
	engine  *Engine
	localID uint32

	mu       sync.Mutex
	remoteID uint32
	opened   bool
	closed   bool
	readBuf  []byte
	readErr  error

	openAck chan error
	ready   chan struct{}
	readCh  chan []byte
}

func newStream(engine *Engine, localID uint32) *Stream {
	return &Stream{
		engine:  engine,
		localID: localID,
		openAck: make(chan error, 1),
		ready:   make(chan struct{}, 1),
		readCh:  make(chan []byte, 16),
	}
}

func (s *Stream) Read(p []byte) (int, error) {
	for len(s.readBuf) == 0 {
		data, ok := <-s.readCh
		if !ok {
			if s.readErr != nil {
				return 0, s.readErr
			}
			return 0, io.EOF
		}
		s.readBuf = data
	}

	n := copy(p, s.readBuf)
	s.readBuf = s.readBuf[n:]
	return n, nil
}

func (s *Stream) Write(p []byte) (int, error) {
	written := 0
	haveReady := false
	defer func() {
		if haveReady {
			s.signalReady()
		}
	}()

	for written < len(p) {
		if !haveReady {
			if err := s.waitReady(); err != nil {
				return written, err
			}
		}
		haveReady = false

		end := written + int(s.engine.maxPayload)
		if end > len(p) {
			end = len(p)
		}
		if err := s.engine.writePacket(adbproto.Packet{
			Command: adbproto.CmdWRTE,
			Arg0:    s.localID,
			Arg1:    s.remote(),
			Payload: p[written:end],
		}); err != nil {
			return written, err
		}
		written = end
		if err := s.waitReady(); err != nil {
			return written, err
		}
		haveReady = true
	}
	return written, nil
}

func (s *Stream) Close() error {
	return s.closeRemote(true)
}

func (s *Stream) ackOpen(err error) {
	select {
	case s.openAck <- err:
	default:
	}
}

func (s *Stream) setRemoteID(remoteID uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := !s.opened
	if remoteID != 0 {
		s.remoteID = remoteID
	}
	s.opened = true
	return first
}

func (s *Stream) remote() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remoteID
}

func (s *Stream) signalReady() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Stream) waitReady() error {
	_, ok := <-s.ready
	if !ok {
		return ErrStreamClosed
	}
	return nil
}

func (s *Stream) deliver(payload []byte) {
	data := append([]byte(nil), payload...)
	s.readCh <- data
}

func (s *Stream) closeRemote(sendCLSE bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	remoteID := s.remoteID
	s.mu.Unlock()

	if sendCLSE && remoteID != 0 {
		_ = s.engine.writePacket(adbproto.Packet{Command: adbproto.CmdCLSE, Arg0: s.localID, Arg1: remoteID})
	}
	s.engine.removeStream(s.localID)
	close(s.ready)
	close(s.readCh)
	return nil
}

func (s *Stream) fail(cause error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.readErr = cause
	s.mu.Unlock()

	s.ackOpen(cause)
	close(s.ready)
	close(s.readCh)
}
