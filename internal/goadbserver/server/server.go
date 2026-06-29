package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/asjdf/goadb/internal/goadbserver/forward"
	"github.com/asjdf/goadb/internal/goadbserver/smartsocket"
	"github.com/asjdf/goadb/internal/goadbserver/transport"
)

const adbServerVersion = "0029"

type Connector interface {
	Connect(ctx context.Context, address string) (transport.Info, error)
}

type ConnectorFunc func(ctx context.Context, address string) (transport.Info, error)

func (f ConnectorFunc) Connect(ctx context.Context, address string) (transport.Info, error) {
	return f(ctx, address)
}

type Options struct {
	Connector      Connector
	Opener         forward.Opener
	ForwardManager *forward.Manager
	Shutdown       func()
}

type Server struct {
	registry       *transport.Registry
	connector      Connector
	opener         forward.Opener
	forwardManager *forward.Manager
	shutdown       func()
}

func New(registry *transport.Registry, opts Options) *Server {
	manager := opts.ForwardManager
	if manager == nil {
		manager = forward.New(opts.Opener)
	}
	return &Server{
		registry:       registry,
		connector:      opts.Connector,
		opener:         opts.Opener,
		forwardManager: manager,
		shutdown:       opts.Shutdown,
	}
}

func (s *Server) ServeConn(ctx context.Context, conn io.ReadWriteCloser) error {
	defer conn.Close()

	var selected *transport.Info
	for {
		request, err := smartsocket.ReadProtocolString(conn)
		if err != nil {
			return err
		}

		if selected != nil {
			keep, err := s.handleTransportRequest(ctx, conn, selected, request)
			if err != nil || !keep {
				return err
			}
			continue
		}

		keep, info, err := s.handleHostRequest(ctx, conn, request)
		if err != nil {
			return err
		}
		if info != nil {
			selected = info
		}
		if !keep {
			return nil
		}
	}
}

func (s *Server) handleHostRequest(ctx context.Context, conn io.Writer, request string) (bool, *transport.Info, error) {
	switch {
	case request == "host:version":
		return false, nil, writeOKAYMessage(conn, adbServerVersion)
	case request == "host:goadb-server":
		return false, nil, writeOKAYMessage(conn, "goadb-server")
	case request == "host:devices":
		return false, nil, writeOKAYMessage(conn, s.registry.ShortList())
	case request == "host:devices-l":
		return false, nil, writeOKAYMessage(conn, s.registry.LongList())
	case request == "host:track-devices":
		return false, nil, s.trackDevices(ctx, conn)
	case request == "host:kill":
		if err := smartsocket.WriteOKAY(conn); err != nil {
			return false, nil, err
		}
		if s.shutdown != nil {
			go s.shutdown()
		}
		return false, nil, nil
	case strings.HasPrefix(request, "host:connect:"):
		return false, nil, s.connect(ctx, conn, strings.TrimPrefix(request, "host:connect:"))
	case strings.HasPrefix(request, "host:disconnect:"):
		return false, nil, s.disconnect(conn, strings.TrimPrefix(request, "host:disconnect:"))
	case request == "host:list-forward":
		return false, nil, writeOKAYMessage(conn, s.forwardManager.ListText())
	case strings.HasPrefix(request, "host:transport"):
		info, err := s.selectTransportRequest(request)
		if err != nil {
			return false, nil, writeFAIL(conn, err)
		}
		if err := smartsocket.WriteOKAY(conn); err != nil {
			return false, nil, err
		}
		return true, &info, nil
	case strings.HasPrefix(request, "host:tport:"):
		info, err := s.selectTransportRequest(request)
		if err != nil {
			return false, nil, writeFAIL(conn, err)
		}
		if err := smartsocket.WriteOKAY(conn); err != nil {
			return false, nil, err
		}
		if err := binary.Write(conn, binary.LittleEndian, info.ID); err != nil {
			return false, nil, err
		}
		return true, &info, nil
	case strings.HasPrefix(request, "host-serial:") || strings.HasPrefix(request, "host-usb:") || strings.HasPrefix(request, "host-local:") || strings.HasPrefix(request, "host:"):
		return false, nil, s.handleDeviceAttribute(conn, request)
	default:
		return false, nil, writeFAIL(conn, fmt.Errorf("unsupported request %q", request))
	}
}

func (s *Server) handleTransportRequest(ctx context.Context, conn io.ReadWriteCloser, selected *transport.Info, request string) (bool, error) {
	switch request {
	case "host:features":
		return false, writeOKAYMessage(conn, strings.Join(selected.Features, ","))
	}
	if strings.HasPrefix(request, "host:forward:") {
		return false, s.addForward(ctx, conn, selected.Serial, strings.TrimPrefix(request, "host:forward:"))
	}
	if strings.HasPrefix(request, "host:killforward:") {
		local := strings.TrimPrefix(request, "host:killforward:")
		s.forwardManager.Remove(local)
		return false, smartsocket.WriteOKAY(conn)
	}
	if request == "host:killforward-all" {
		s.forwardManager.RemoveAll()
		return false, smartsocket.WriteOKAY(conn)
	}
	if request == "remount" {
		return false, s.proxyRemoteSingleResponse(ctx, conn, selected.Serial, "remount:")
	}
	if isRemoteService(request) {
		return false, s.proxyRemoteService(ctx, conn, selected.Serial, request)
	}
	return false, writeFAIL(conn, fmt.Errorf("unsupported transport request %q", request))
}

func (s *Server) trackDevices(ctx context.Context, conn io.Writer) error {
	sub := s.registry.Subscribe()
	defer sub.Cancel()

	if err := smartsocket.WriteOKAY(conn); err != nil {
		return err
	}

	for {
		select {
		case snapshot, ok := <-sub.C:
			if !ok {
				return nil
			}
			if err := smartsocket.WriteProtocolString(conn, transport.FormatShortList(snapshot)); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) connect(ctx context.Context, conn io.Writer, address string) error {
	if !strings.Contains(address, ":") {
		address += ":5555"
	}
	if s.connector == nil {
		return writeFAIL(conn, fmt.Errorf("no tcp connector configured"))
	}
	info, err := s.connector.Connect(ctx, address)
	if err != nil {
		return writeFAIL(conn, err)
	}
	s.registry.Upsert(info)
	return writeOKAYMessage(conn, "connected to "+address)
}

func (s *Server) disconnect(conn io.Writer, address string) error {
	if !strings.Contains(address, ":") && address != "" {
		address += ":5555"
	}
	if address == "" {
		for _, info := range s.registry.Snapshot() {
			if info.Type == transport.TypeTCP {
				s.registry.Remove(info.Serial)
			}
		}
		return writeOKAYMessage(conn, "disconnected everything")
	}
	s.registry.Remove(address)
	return writeOKAYMessage(conn, "disconnected "+address)
}

func (s *Server) selectTransportRequest(request string) (transport.Info, error) {
	switch {
	case request == "host:transport-any":
		return s.registry.Select(transport.AnySelector())
	case request == "host:transport-usb":
		return s.registry.Select(transport.USBSelector())
	case request == "host:transport-local":
		return s.registry.Select(transport.LocalSelector())
	case strings.HasPrefix(request, "host:transport:"):
		return s.registry.Select(transport.SerialSelector(strings.TrimPrefix(request, "host:transport:")))
	case request == "host:tport:any":
		return s.registry.Select(transport.AnySelector())
	case request == "host:tport:usb":
		return s.registry.Select(transport.USBSelector())
	case request == "host:tport:local":
		return s.registry.Select(transport.LocalSelector())
	case strings.HasPrefix(request, "host:tport:serial:"):
		return s.registry.Select(transport.SerialSelector(strings.TrimPrefix(request, "host:tport:serial:")))
	default:
		return transport.Info{}, fmt.Errorf("unsupported transport request %q", request)
	}
}

func (s *Server) handleDeviceAttribute(conn io.Writer, request string) error {
	selector, attr, err := parseDeviceAttributeRequest(request)
	if err != nil {
		return writeFAIL(conn, err)
	}

	if attr != "get-state" && attr != "get-serialno" && attr != "get-devpath" && attr != "features" {
		return writeFAIL(conn, fmt.Errorf("unsupported device attribute %q", attr))
	}

	info, err := s.findForAttribute(selector)
	if err != nil {
		return writeFAIL(conn, err)
	}

	switch attr {
	case "get-state":
		return writeOKAYMessage(conn, string(info.State))
	case "get-serialno":
		return writeOKAYMessage(conn, info.Serial)
	case "get-devpath":
		return writeOKAYMessage(conn, info.DevPath)
	case "features":
		return writeOKAYMessage(conn, strings.Join(info.Features, ","))
	default:
		return writeFAIL(conn, fmt.Errorf("unsupported device attribute %q", attr))
	}
}

func (s *Server) addForward(ctx context.Context, conn io.Writer, serial string, spec string) error {
	norebind := false
	if strings.HasPrefix(spec, "norebind:") {
		norebind = true
		spec = strings.TrimPrefix(spec, "norebind:")
	}
	local, remote, ok := strings.Cut(spec, ";")
	if !ok {
		return writeFAIL(conn, fmt.Errorf("malformed forward request %q", spec))
	}
	if _, err := s.forwardManager.Add(ctx, serial, local, remote, norebind); err != nil {
		return writeFAIL(conn, err)
	}
	return smartsocket.WriteOKAY(conn)
}

func (s *Server) proxyRemoteService(ctx context.Context, conn io.ReadWriteCloser, serial string, service string) error {
	if s.opener == nil {
		return writeFAIL(conn, fmt.Errorf("no stream opener configured"))
	}
	stream, err := s.opener.Open(ctx, serial, service)
	if err != nil {
		return writeFAIL(conn, err)
	}
	if err := smartsocket.WriteOKAY(conn); err != nil {
		_ = stream.Close()
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(stream, conn)
		_ = stream.Close()
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(conn, stream)
		_ = conn.Close()
		_ = stream.Close()
		errCh <- err
	}()
	err = <-errCh
	if isBenignProxyCloseError(err) {
		return nil
	}
	return err
}

func isBenignProxyCloseError(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

func (s *Server) proxyRemoteSingleResponse(ctx context.Context, conn io.Writer, serial string, service string) error {
	if s.opener == nil {
		return writeFAIL(conn, fmt.Errorf("no stream opener configured"))
	}
	stream, err := s.opener.Open(ctx, serial, service)
	if err != nil {
		return writeFAIL(conn, err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return writeFAIL(conn, err)
	}
	return writeOKAYMessage(conn, string(data))
}

func isRemoteService(request string) bool {
	return strings.HasPrefix(request, "shell:") ||
		strings.HasPrefix(request, "shell,") ||
		strings.HasPrefix(request, "exec:") ||
		request == "sync:" ||
		request == "remount" ||
		strings.HasPrefix(request, "tcp:") ||
		strings.HasPrefix(request, "local")
}

func parseDeviceAttributeRequest(request string) (transport.Selector, string, error) {
	switch {
	case strings.HasPrefix(request, "host-serial:"):
		rest := strings.TrimPrefix(request, "host-serial:")
		idx := strings.LastIndex(rest, ":")
		if idx <= 0 || idx == len(rest)-1 {
			return transport.Selector{}, "", fmt.Errorf("malformed host-serial request %q", request)
		}
		serial, attr := rest[:idx], rest[idx+1:]
		return transport.SerialSelector(serial), attr, nil
	case strings.HasPrefix(request, "host-usb:"):
		return transport.USBSelector(), strings.TrimPrefix(request, "host-usb:"), nil
	case strings.HasPrefix(request, "host-local:"):
		return transport.LocalSelector(), strings.TrimPrefix(request, "host-local:"), nil
	case strings.HasPrefix(request, "host:"):
		return transport.AnySelector(), strings.TrimPrefix(request, "host:"), nil
	default:
		return transport.Selector{}, "", fmt.Errorf("malformed device request %q", request)
	}
}

func (s *Server) findForAttribute(selector transport.Selector) (transport.Info, error) {
	var matches []transport.Info
	for _, info := range s.registry.Snapshot() {
		switch selector.Kind {
		case transport.SelectorAny:
			matches = append(matches, info)
		case transport.SelectorUSB:
			if info.Type == transport.TypeUSB {
				matches = append(matches, info)
			}
		case transport.SelectorLocal:
			if info.Type == transport.TypeTCP {
				matches = append(matches, info)
			}
		case transport.SelectorSerial:
			if info.Serial == selector.Serial {
				matches = append(matches, info)
			}
		}
	}

	if len(matches) == 0 {
		return transport.Info{}, transport.ErrTransportNotFound
	}
	if len(matches) > 1 {
		return transport.Info{}, transport.ErrAmbiguousTransport
	}
	return matches[0], nil
}

func writeOKAYMessage(w io.Writer, message string) error {
	if err := smartsocket.WriteOKAY(w); err != nil {
		return err
	}
	return smartsocket.WriteProtocolString(w, message)
}

func writeFAIL(w io.Writer, err error) error {
	return smartsocket.WriteFAIL(w, err.Error())
}
