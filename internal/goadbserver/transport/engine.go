package transport

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/asjdf/goadb/internal/goadbserver/adbproto"
	"github.com/asjdf/goadb/internal/goadbserver/auth"
)

type Options struct {
	Serial       string
	Type         Type
	KeyPair      *auth.KeyPair
	HostFeatures []string
}

type Engine struct {
	conn io.ReadWriteCloser
	opts Options

	writeMu sync.Mutex
	mu      sync.Mutex

	maxPayload uint32
	nextLocal  uint32
	streams    map[uint32]*Stream
	closed     bool
	readDone   chan struct{}
}

func NewEngine(conn io.ReadWriteCloser, opts Options) *Engine {
	return &Engine{
		conn:       conn,
		opts:       opts,
		maxPayload: adbproto.MaxPayload,
		nextLocal:  1,
		streams:    make(map[uint32]*Stream),
		readDone:   make(chan struct{}),
	}
}

func (e *Engine) Connect(ctx context.Context) (Info, error) {
	if err := e.writePacket(adbproto.Packet{
		Command: adbproto.CmdCNXN,
		Arg0:    adbproto.Version,
		Arg1:    adbproto.MaxPayload,
		Payload: []byte(e.hostBanner()),
	}); err != nil {
		return Info{}, err
	}

	sentSignature := false
	for {
		if err := ctx.Err(); err != nil {
			return Info{}, err
		}

		packet, err := adbproto.ReadPacket(e.conn, adbproto.MaxPayload)
		if err != nil {
			return Info{}, err
		}

		switch packet.Command {
		case adbproto.CmdAUTH:
			if packet.Arg0 != adbproto.AuthToken {
				return Info{}, fmt.Errorf("unexpected AUTH type %d", packet.Arg0)
			}
			if e.opts.KeyPair == nil {
				return Info{}, fmt.Errorf("device requires adb auth but no key pair is configured")
			}
			if sentSignature && e.opts.KeyPair.PublicKey != "" {
				payload := append([]byte(e.opts.KeyPair.PublicKey), 0)
				if err := e.writePacket(adbproto.Packet{Command: adbproto.CmdAUTH, Arg0: adbproto.AuthPublicKey, Payload: payload}); err != nil {
					return Info{}, err
				}
				continue
			}
			signature, err := e.opts.KeyPair.SignToken(packet.Payload)
			if err != nil {
				return Info{}, err
			}
			sentSignature = true
			if err := e.writePacket(adbproto.Packet{Command: adbproto.CmdAUTH, Arg0: adbproto.AuthSignature, Payload: signature}); err != nil {
				return Info{}, err
			}

		case adbproto.CmdCNXN:
			e.maxPayload = minPayload(packet.Arg1, adbproto.MaxPayload)
			info := parseDeviceBanner(packet.Payload, e.opts.Serial, e.opts.Type)
			go e.readLoop()
			return info, nil

		case adbproto.CmdCLSE:
			continue

		default:
			return Info{}, fmt.Errorf("unexpected packet during connect: %#x", packet.Command)
		}
	}
}

func (e *Engine) Open(ctx context.Context, service string) (*Stream, error) {
	stream := e.newStream()
	payload := append([]byte(service), 0)
	if err := e.writePacket(adbproto.Packet{Command: adbproto.CmdOPEN, Arg0: stream.localID, Payload: payload}); err != nil {
		e.removeStream(stream.localID)
		return nil, err
	}

	select {
	case err := <-stream.openAck:
		if err != nil {
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		e.removeStream(stream.localID)
		return nil, ctx.Err()
	}
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	streams := make([]*Stream, 0, len(e.streams))
	for _, stream := range e.streams {
		streams = append(streams, stream)
	}
	e.mu.Unlock()

	for _, stream := range streams {
		stream.closeRemote(false)
	}
	return e.conn.Close()
}

func (e *Engine) writePacket(packet adbproto.Packet) error {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	return adbproto.WritePacket(e.conn, packet)
}

func (e *Engine) readLoop() {
	defer close(e.readDone)
	for {
		packet, err := adbproto.ReadPacket(e.conn, e.maxPayload)
		if err != nil {
			e.closeAllStreams(err)
			return
		}
		switch packet.Command {
		case adbproto.CmdOKAY:
			e.handleOKAY(packet)
		case adbproto.CmdWRTE:
			e.handleWRTE(packet)
		case adbproto.CmdCLSE:
			e.handleCLSE(packet)
		}
	}
}

func (e *Engine) newStream() *Stream {
	e.mu.Lock()
	defer e.mu.Unlock()

	localID := e.nextLocal
	e.nextLocal++
	stream := newStream(e, localID)
	e.streams[localID] = stream
	return stream
}

func (e *Engine) stream(localID uint32) *Stream {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.streams[localID]
}

func (e *Engine) removeStream(localID uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.streams, localID)
}

func (e *Engine) handleOKAY(packet adbproto.Packet) {
	stream := e.stream(packet.Arg1)
	if stream == nil {
		return
	}
	firstReady := stream.setRemoteID(packet.Arg0)
	if firstReady {
		stream.ackOpen(nil)
	}
	stream.signalReady()
}

func (e *Engine) handleWRTE(packet adbproto.Packet) {
	stream := e.stream(packet.Arg1)
	if stream == nil {
		return
	}
	stream.setRemoteID(packet.Arg0)
	_ = e.writePacket(adbproto.Packet{Command: adbproto.CmdOKAY, Arg0: stream.localID, Arg1: packet.Arg0})
	stream.deliver(packet.Payload)
}

func (e *Engine) handleCLSE(packet adbproto.Packet) {
	stream := e.stream(packet.Arg1)
	if stream == nil {
		return
	}
	stream.closeRemote(false)
	e.removeStream(packet.Arg1)
}

func (e *Engine) closeAllStreams(cause error) {
	e.mu.Lock()
	streams := make([]*Stream, 0, len(e.streams))
	for _, stream := range e.streams {
		streams = append(streams, stream)
	}
	e.streams = make(map[uint32]*Stream)
	e.mu.Unlock()

	for _, stream := range streams {
		stream.fail(cause)
	}
}

func (e *Engine) hostBanner() string {
	features := e.opts.HostFeatures
	if len(features) == 0 {
		features = []string{"shell_v2", "cmd", "stat_v2", "ls_v2", "fixed_push_mkdir", "apex", "abb", "abb_exec", "sendrecv_v2"}
	}
	return "host::features=" + strings.Join(features, ",")
}

func parseDeviceBanner(payload []byte, serial string, transportType Type) Info {
	banner := strings.TrimRight(string(payload), "\x00")
	info := Info{Serial: serial, Type: transportType, State: StateDevice}

	if idx := strings.Index(banner, "::"); idx >= 0 {
		banner = banner[idx+2:]
	}
	for _, field := range strings.Split(banner, ";") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "ro.product.name":
			info.Product = value
		case "ro.product.model":
			info.Model = value
		case "ro.product.device":
			info.Device = value
		case "features":
			if value != "" {
				info.Features = strings.Split(value, ",")
			}
		}
	}
	return info
}

func minPayload(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
