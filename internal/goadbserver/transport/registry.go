package transport

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu          sync.Mutex
	nextID      uint64
	transports  map[string]Info
	subscribers map[*Subscription]chan []Info
}

type Subscription struct {
	C      <-chan []Info
	cancel func()
}

func NewRegistry() *Registry {
	return &Registry{
		nextID:      1,
		transports:  make(map[string]Info),
		subscribers: make(map[*Subscription]chan []Info),
	}
}

func (r *Registry) Upsert(info Info) Info {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.transports[info.Serial]; ok {
		info.ID = existing.ID
	} else if info.ID == 0 {
		info.ID = r.nextID
		r.nextID++
	}
	r.transports[info.Serial] = cloneInfo(info)
	r.broadcastLocked()
	return info
}

func (r *Registry) Remove(serial string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.transports[serial]; !ok {
		return false
	}
	delete(r.transports, serial)
	r.broadcastLocked()
	return true
}

func (r *Registry) Snapshot() []Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *Registry) ShortList() string {
	return FormatShortList(r.Snapshot())
}

func (r *Registry) LongList() string {
	return FormatLongList(r.Snapshot())
}

func (r *Registry) Select(selector Selector) (Info, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var matches []Info
	for _, info := range r.transports {
		if info.State != StateDevice {
			continue
		}
		switch selector.Kind {
		case SelectorAny:
			matches = append(matches, info)
		case SelectorUSB:
			if info.Type == TypeUSB {
				matches = append(matches, info)
			}
		case SelectorLocal:
			if info.Type == TypeTCP {
				matches = append(matches, info)
			}
		case SelectorSerial:
			if info.Serial == selector.Serial {
				matches = append(matches, info)
			}
		}
	}
	sortInfos(matches)

	if len(matches) == 0 {
		return Info{}, ErrTransportNotFound
	}
	if len(matches) > 1 {
		return Info{}, ErrAmbiguousTransport
	}
	return cloneInfo(matches[0]), nil
}

func (r *Registry) Subscribe() *Subscription {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch := make(chan []Info, 16)
	sub := &Subscription{}
	sub.C = ch
	sub.cancel = func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if subscriberCh, ok := r.subscribers[sub]; ok {
			delete(r.subscribers, sub)
			close(subscriberCh)
		}
	}
	r.subscribers[sub] = ch
	ch <- r.snapshotLocked()
	return sub
}

func (s *Subscription) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}

func FormatShortList(infos []Info) string {
	sortInfos(infos)
	var b strings.Builder
	for _, info := range infos {
		fmt.Fprintf(&b, "%s\t%s\n", info.Serial, info.State)
	}
	return b.String()
}

func FormatLongList(infos []Info) string {
	sortInfos(infos)
	var b strings.Builder
	for _, info := range infos {
		fmt.Fprintf(&b, "%s %s", info.Serial, info.State)
		if info.DevPath != "" {
			fmt.Fprintf(&b, " usb:%s", info.DevPath)
		}
		if info.Product != "" {
			fmt.Fprintf(&b, " product:%s", sanitizeAttribute(info.Product))
		}
		if info.Model != "" {
			fmt.Fprintf(&b, " model:%s", sanitizeAttribute(info.Model))
		}
		if info.Device != "" {
			fmt.Fprintf(&b, " device:%s", sanitizeAttribute(info.Device))
		}
		fmt.Fprintf(&b, " transport_id:%d\n", info.ID)
	}
	return b.String()
}

func (r *Registry) broadcastLocked() {
	snapshot := r.snapshotLocked()
	for _, ch := range r.subscribers {
		select {
		case ch <- snapshot:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- snapshot
		}
	}
}

func (r *Registry) snapshotLocked() []Info {
	infos := make([]Info, 0, len(r.transports))
	for _, info := range r.transports {
		infos = append(infos, cloneInfo(info))
	}
	sortInfos(infos)
	return infos
}

func sortInfos(infos []Info) {
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Serial < infos[j].Serial
	})
}

func cloneInfo(info Info) Info {
	info.Features = append([]string(nil), info.Features...)
	return info
}

func sanitizeAttribute(value string) string {
	value = strings.ReplaceAll(value, "\n", "_")
	value = strings.ReplaceAll(value, "\t", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
