package tcp

import (
	"context"
	"net"
	"sync"

	"github.com/asjdf/goadb/internal/goadbserver/auth"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

type Dialer func(ctx context.Context, address string) (net.Conn, error)

type Options struct {
	KeyPair *auth.KeyPair
	Dialer  Dialer
}

type Backend struct {
	keyPair *auth.KeyPair
	dialer  Dialer

	mu      sync.Mutex
	engines map[string]*transport.Engine
}

func New(opts Options) *Backend {
	dialer := opts.Dialer
	if dialer == nil {
		var netDialer net.Dialer
		dialer = func(ctx context.Context, address string) (net.Conn, error) {
			return netDialer.DialContext(ctx, "tcp", address)
		}
	}
	return &Backend{
		keyPair: opts.KeyPair,
		dialer:  dialer,
		engines: make(map[string]*transport.Engine),
	}
}

func (b *Backend) Connect(ctx context.Context, address string) (transport.Info, error) {
	conn, err := b.dialer(ctx, address)
	if err != nil {
		return transport.Info{}, err
	}

	engine := transport.NewEngine(conn, transport.Options{
		Serial:  address,
		Type:    transport.TypeTCP,
		KeyPair: b.keyPair,
	})
	info, err := engine.Connect(ctx)
	if err != nil {
		conn.Close()
		return transport.Info{}, err
	}

	b.mu.Lock()
	if existing := b.engines[address]; existing != nil {
		_ = existing.Close()
	}
	b.engines[address] = engine
	b.mu.Unlock()

	return info, nil
}

func (b *Backend) Open(ctx context.Context, serial string, service string) (*transport.Stream, error) {
	b.mu.Lock()
	engine := b.engines[serial]
	b.mu.Unlock()
	if engine == nil {
		return nil, transport.ErrTransportNotFound
	}
	return engine.Open(ctx, service)
}

func (b *Backend) Close(serial string) {
	b.mu.Lock()
	engine := b.engines[serial]
	delete(b.engines, serial)
	b.mu.Unlock()
	if engine != nil {
		_ = engine.Close()
	}
}
