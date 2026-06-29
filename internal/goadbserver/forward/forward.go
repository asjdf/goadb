package forward

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Opener interface {
	Open(ctx context.Context, serial string, service string) (io.ReadWriteCloser, error)
}

type OpenerFunc func(ctx context.Context, serial string, service string) (io.ReadWriteCloser, error)

func (f OpenerFunc) Open(ctx context.Context, serial string, service string) (io.ReadWriteCloser, error) {
	return f(ctx, serial, service)
}

type Rule struct {
	Serial string
	Local  string
	Remote string
}

type Manager struct {
	opener Opener

	mu        sync.Mutex
	listeners map[string]*listenerRule
}

type listenerRule struct {
	Rule
	listener net.Listener
}

func New(opener Opener) *Manager {
	return &Manager{
		opener:    opener,
		listeners: make(map[string]*listenerRule),
	}
}

func (m *Manager) Add(ctx context.Context, serial, local, remote string, norebind bool) (Rule, error) {
	listenAddr, err := tcpListenAddress(local)
	if err != nil {
		return Rule{}, err
	}

	m.mu.Lock()
	if existing := m.listeners[local]; existing != nil && norebind {
		m.mu.Unlock()
		return Rule{}, fmt.Errorf("forward already exists for %s", local)
	}
	if existing := m.listeners[local]; existing != nil {
		delete(m.listeners, local)
		_ = existing.listener.Close()
	}
	m.mu.Unlock()

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return Rule{}, err
	}

	actualLocal := local
	if strings.HasSuffix(local, ":0") || local == "0" {
		actualLocal = "tcp:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	}

	rule := &listenerRule{
		Rule:     Rule{Serial: serial, Local: actualLocal, Remote: remote},
		listener: listener,
	}

	m.mu.Lock()
	if existing := m.listeners[actualLocal]; existing != nil && norebind {
		m.mu.Unlock()
		_ = listener.Close()
		return Rule{}, fmt.Errorf("forward already exists for %s", actualLocal)
	}
	m.listeners[actualLocal] = rule
	m.mu.Unlock()

	go m.acceptLoop(ctx, rule)
	return rule.Rule, nil
}

func (m *Manager) Remove(local string) bool {
	m.mu.Lock()
	rule := m.listeners[local]
	if rule != nil {
		delete(m.listeners, local)
	}
	m.mu.Unlock()
	if rule == nil {
		return false
	}
	_ = rule.listener.Close()
	return true
}

func (m *Manager) RemoveAll() {
	m.mu.Lock()
	rules := make([]*listenerRule, 0, len(m.listeners))
	for _, rule := range m.listeners {
		rules = append(rules, rule)
	}
	m.listeners = make(map[string]*listenerRule)
	m.mu.Unlock()

	for _, rule := range rules {
		_ = rule.listener.Close()
	}
}

func (m *Manager) List() []Rule {
	m.mu.Lock()
	defer m.mu.Unlock()

	rules := make([]Rule, 0, len(m.listeners))
	for _, rule := range m.listeners {
		rules = append(rules, rule.Rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Serial != rules[j].Serial {
			return rules[i].Serial < rules[j].Serial
		}
		return rules[i].Local < rules[j].Local
	})
	return rules
}

func (m *Manager) ListText() string {
	var b strings.Builder
	for _, rule := range m.List() {
		fmt.Fprintf(&b, "%s %s %s\n", rule.Serial, rule.Local, rule.Remote)
	}
	return b.String()
}

func (m *Manager) acceptLoop(ctx context.Context, rule *listenerRule) {
	for {
		localConn, err := rule.listener.Accept()
		if err != nil {
			return
		}
		go m.bridge(ctx, rule.Rule, localConn)
	}
}

func (m *Manager) bridge(ctx context.Context, rule Rule, localConn net.Conn) {
	if m.opener == nil {
		_ = localConn.Close()
		return
	}

	remoteConn, err := m.opener.Open(ctx, rule.Serial, rule.Remote)
	if err != nil {
		_ = localConn.Close()
		return
	}

	go copyAndClose(remoteConn, localConn)
	go copyAndClose(localConn, remoteConn)
}

func copyAndClose(dst io.WriteCloser, src io.ReadCloser) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
}

func tcpListenAddress(endpoint string) (string, error) {
	port := endpoint
	if strings.HasPrefix(endpoint, "tcp:") {
		port = strings.TrimPrefix(endpoint, "tcp:")
	}
	if port == "" {
		return "", fmt.Errorf("missing tcp port in %q", endpoint)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("unsupported forward endpoint %q", endpoint)
	}
	return "127.0.0.1:" + port, nil
}
