package transport

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mux puts several messengers behind one Transport. Ids from the unprefixed
// child (Telegram) pass through as they are; every other child's chat and
// callback ids get "<key>:" in front ("wa:380…@s.whatsapp.net"), and Send
// and Ack route on that prefix. Telegram's ids are numbers, so an id without
// a colon is always the unprefixed child's and a prefixed one never is.
//
// Every Send is bounded by SendTimeout, and a child whose last status was
// down fails at once with ErrDown, so one messenger that hangs or is gone
// never holds up the core or the others.
type Mux struct {
	// SendTimeout bounds each Send and Ack; zero means the package's
	// SendTimeout.
	SendTimeout time.Duration

	unprefixed string
	children   map[string]Transport
	keys       []string // sorted, for a stable start order

	mu sync.Mutex
	// down is the fail-fast flag, per child key.
	down map[string]bool
	// latest is the last status per child that the status loop has not
	// passed on yet; a newer status replaces an unread older one.
	latest map[string]Status
	notify chan struct{}
	status chan Status
}

var (
	_ Transport = (*Mux)(nil)
	_ Capable   = (*Mux)(nil)
	_ Reporter  = (*Mux)(nil)
)

// NewMux returns a Mux over children; the child under unprefixed keeps its
// raw ids.
func NewMux(unprefixed string, children map[string]Transport) *Mux {
	m := &Mux{
		SendTimeout: SendTimeout,
		unprefixed:  unprefixed,
		children:    children,
		down:        map[string]bool{},
		latest:      map[string]Status{},
		notify:      make(chan struct{}, 1),
		status:      make(chan Status),
	}
	for k := range children {
		m.keys = append(m.keys, k)
	}
	sort.Strings(m.keys)
	return m
}

// Receive starts every child and merges their messages, keeping each
// child's order. The merged channel closes once every child has closed its
// own or ctx has ended. It also starts the status loop, which ends with ctx.
func (m *Mux) Receive(ctx context.Context) (<-chan Incoming, error) {
	ctx, cancel := context.WithCancel(ctx)
	ins := make(map[string]<-chan Incoming, len(m.keys))
	for _, k := range m.keys {
		in, err := m.children[k].Receive(ctx)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		ins[k] = in
	}
	out := make(chan Incoming)
	var wg sync.WaitGroup
	for _, k := range m.keys {
		wg.Add(1)
		go func(k string, in <-chan Incoming) {
			defer wg.Done()
			for {
				var msg Incoming
				select {
				case got, ok := <-in:
					if !ok {
						return
					}
					msg = got
				case <-ctx.Done():
					return
				}
				select {
				case out <- m.prefix(k, msg):
				case <-ctx.Done():
					return
				}
			}
		}(k, ins[k])
	}
	go func() {
		wg.Wait()
		cancel()
		close(out)
	}()
	for _, k := range m.keys {
		if r, ok := m.children[k].(Reporter); ok {
			go m.watch(ctx, k, r.Status())
		}
	}
	go m.emit(ctx)
	return out, nil
}

func (m *Mux) prefix(k string, msg Incoming) Incoming {
	if k == m.unprefixed {
		return msg
	}
	msg.Chat = ChatID(k + ":" + string(msg.Chat))
	if msg.Callback != nil {
		cb := *msg.Callback
		cb.ID = k + ":" + cb.ID
		msg.Callback = &cb
	}
	return msg
}

// route finds the child for an id and the id in that child's own terms.
func (m *Mux) route(id string) (string, string, error) {
	k, rest, ok := strings.Cut(id, ":")
	if !ok {
		k, rest = m.unprefixed, id
	} else if k == m.unprefixed {
		return "", "", fmt.Errorf("transport: unknown messenger in %q", id)
	}
	if _, found := m.children[k]; !found {
		return "", "", fmt.Errorf("transport: unknown messenger in %q", id)
	}
	return k, rest, nil
}

// child returns the child for an id, or ErrDown when it last reported
// itself down.
func (m *Mux) child(id string) (Transport, string, error) {
	k, rest, err := m.route(id)
	if err != nil {
		return nil, "", err
	}
	m.mu.Lock()
	down := m.down[k]
	m.mu.Unlock()
	if down {
		return nil, "", fmt.Errorf("%s: %w", k, ErrDown)
	}
	return m.children[k], rest, nil
}

func (m *Mux) timeout() time.Duration {
	if m.SendTimeout <= 0 {
		return SendTimeout
	}
	return m.SendTimeout
}

// Send routes a message by the chat's prefix, within SendTimeout.
func (m *Mux) Send(ctx context.Context, chat ChatID, msg Outgoing) (MsgRef, error) {
	tr, id, err := m.child(string(chat))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	return tr.Send(ctx, ChatID(id), msg)
}

// Ack routes a button press by the callback id's prefix, within
// SendTimeout.
func (m *Mux) Ack(ctx context.Context, cb Callback, text string) error {
	tr, id, err := m.child(cb.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	cb.ID = id
	return tr.Ack(ctx, cb, text)
}

// Caps returns the chat's child's capabilities; TelegramCaps for a child
// that is not Capable or a chat no child owns.
func (m *Mux) Caps(chat ChatID) Caps {
	k, rest, err := m.route(string(chat))
	if err != nil {
		return TelegramCaps
	}
	if c, ok := m.children[k].(Capable); ok {
		return c.Caps(ChatID(rest))
	}
	return TelegramCaps
}

// Status merges the children's statuses, Messenger set to the child's key.
// A slow reader only delays this channel: the children are never blocked,
// and a newer status of a messenger replaces its unread older one.
func (m *Mux) Status() <-chan Status { return m.status }

// watch reads one child's statuses. It sets the fail-fast flag first, so
// that Send stops at once, then leaves the status for emit.
func (m *Mux) watch(ctx context.Context, k string, ch <-chan Status) {
	for {
		select {
		case <-ctx.Done():
			return
		case st, ok := <-ch:
			if !ok {
				return
			}
			st.Messenger = k
			m.mu.Lock()
			m.down[k] = !st.Up
			m.latest[k] = st
			m.mu.Unlock()
			select {
			case m.notify <- struct{}{}:
			default:
			}
		}
	}
}

// emit is the only goroutine that blocks on the Status channel.
func (m *Mux) emit(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.notify:
		}
		m.mu.Lock()
		var pending []Status
		for _, k := range m.keys {
			if st, ok := m.latest[k]; ok {
				pending = append(pending, st)
				delete(m.latest, k)
			}
		}
		m.mu.Unlock()
		for _, st := range pending {
			select {
			case m.status <- st:
			case <-ctx.Done():
				return
			}
		}
	}
}
