package transport

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory Transport for tests: the test pushes Incoming
// messages in and reads what the core sent out.
type Fake struct {
	mu   sync.Mutex
	in   chan Incoming
	Sent []Sent
	Acks []Callback
	// FailSend, when set, makes every Send return it.
	FailSend error
	// FailChat makes Send fail for one chat only.
	FailChat map[ChatID]error
	// Hang makes Send for a chat block until its context ends, like a
	// messenger that stopped answering.
	Hang map[ChatID]bool
	// Statuses, when set, is what Status returns; the test sends on it.
	// Nil never fires.
	Statuses chan Status
}

// Sent is one message the core sent.
type Sent struct {
	Chat ChatID
	Msg  Outgoing
}

// NewFake returns a Fake with a buffered inbox.
func NewFake() *Fake {
	return &Fake{in: make(chan Incoming, 64), FailChat: map[ChatID]error{}, Hang: map[ChatID]bool{}}
}

// Push delivers an Incoming to the core.
func (f *Fake) Push(in Incoming) { f.in <- in }

// Close ends the inbox, which ends the core's loop.
func (f *Fake) Close() { close(f.in) }

func (f *Fake) Receive(context.Context) (<-chan Incoming, error) { return f.in, nil }

func (f *Fake) Send(ctx context.Context, chat ChatID, msg Outgoing) (MsgRef, error) {
	f.mu.Lock()
	hang := f.Hang[chat]
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailSend != nil {
		return "", f.FailSend
	}
	if err := f.FailChat[chat]; err != nil {
		return "", err
	}
	f.Sent = append(f.Sent, Sent{Chat: chat, Msg: msg})
	return MsgRef(fmt.Sprintf("m%d", len(f.Sent))), nil
}

func (f *Fake) Ack(_ context.Context, cb Callback, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Acks = append(f.Acks, cb)
	return nil
}

// Status makes a Fake a Reporter.
func (f *Fake) Status() <-chan Status { return f.Statuses }

// Last returns the most recent sent message, or an empty Sent.
func (f *Fake) Last() Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Sent) == 0 {
		return Sent{}
	}
	return f.Sent[len(f.Sent)-1]
}

// Count returns how many messages were sent.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Sent)
}

// Since returns the messages sent after the first n.
func (f *Fake) Since(n int) []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sent(nil), f.Sent[n:]...)
}
