package transport

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const waChat = "380501234567@s.whatsapp.net"

func newMux() (*Mux, *Fake, *Fake) {
	tg, wa := NewFake(), NewFake()
	return NewMux("tg", map[string]Transport{"tg": tg, "wa": wa}), tg, wa
}

func TestMuxRoutesSendAndAck(t *testing.T) {
	m, tg, wa := newMux()
	ctx := context.Background()
	if _, err := m.Send(ctx, "-100123", Outgoing{Text: "tg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(ctx, "wa:"+waChat, Outgoing{Text: "wa"}); err != nil {
		t.Fatal(err)
	}
	if tg.Count() != 1 || tg.Last().Chat != "-100123" || tg.Last().Msg.Text != "tg" {
		t.Errorf("tg sent %+v", tg.Sent)
	}
	if wa.Count() != 1 || wa.Last().Chat != waChat || wa.Last().Msg.Text != "wa" {
		t.Errorf("wa sent %+v", wa.Sent)
	}
	if err := m.Ack(ctx, Callback{ID: "4242", Data: "t:todo:1"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Ack(ctx, Callback{ID: "wa:3EB0", Data: "a:todo:1"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(tg.Acks) != 1 || tg.Acks[0].ID != "4242" || len(wa.Acks) != 1 || wa.Acks[0].ID != "3EB0" || wa.Acks[0].Data != "a:todo:1" {
		t.Errorf("acks tg=%+v wa=%+v", tg.Acks, wa.Acks)
	}
	for _, bad := range []ChatID{"xx:1", "tg:-100123", ":1"} {
		if _, err := m.Send(ctx, bad, Outgoing{Text: "x"}); err == nil {
			t.Errorf("Send(%q): want unknown messenger", bad)
		}
		if err := m.Ack(ctx, Callback{ID: string(bad)}, ""); err == nil {
			t.Errorf("Ack(%q): want unknown messenger", bad)
		}
	}
	if tg.Count() != 1 || wa.Count() != 1 {
		t.Error("a bad id reached a child")
	}
	if c := m.Caps("wa:" + waChat); c != TelegramCaps {
		t.Errorf("Caps of a child that is not Capable = %+v", c)
	}
}

func TestMuxReceivePrefixesAndKeepsOrder(t *testing.T) {
	m, tg, wa := newMux()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, err := m.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const n = 20
	for i := 0; i < n; i++ {
		tg.Push(Incoming{Chat: "-100123", User: "7", Text: fmt.Sprint(i)})
		wa.Push(Incoming{Chat: waChat, User: "380501234567", Text: fmt.Sprint(i)})
	}
	wa.Push(Incoming{Chat: waChat, User: "380501234567", Callback: &Callback{ID: "3EB0", Data: "a:todo:1"}})
	tg.Close()
	next := map[ChatID]int{}
	for i := 0; i < 2*n+1; i++ {
		msg := recv(t, in)
		if msg.Callback != nil {
			if msg.Chat != "wa:"+waChat || msg.Callback.ID != "wa:3EB0" || next["wa:"+waChat] != n {
				t.Errorf("callback = %+v %+v", msg, msg.Callback)
			}
			continue
		}
		if msg.Chat != "-100123" && msg.Chat != "wa:"+waChat {
			t.Fatalf("chat %q", msg.Chat)
		}
		if msg.Text != fmt.Sprint(next[msg.Chat]) {
			t.Errorf("%s: got %q, want %d", msg.Chat, msg.Text, next[msg.Chat])
		}
		next[msg.Chat]++
	}
	// tg has closed; the merged channel stays open for wa.
	wa.Push(Incoming{Chat: waChat, Text: "late"})
	if msg := recv(t, in); msg.Text != "late" {
		t.Errorf("after tg closed: %+v", msg)
	}
	wa.Close()
	select {
	case _, ok := <-in:
		if ok {
			t.Error("want closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("merged channel not closed after every child closed")
	}
}

func TestMuxReceiveEndsWithContext(t *testing.T) {
	m, _, _ := newMux()
	ctx, cancel := context.WithCancel(context.Background())
	in, err := m.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-in:
		if ok {
			t.Error("want closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("merged channel not closed after ctx ended")
	}
}

func TestMuxHangBoundedBySendTimeout(t *testing.T) {
	m, tg, wa := newMux()
	m.SendTimeout = 50 * time.Millisecond
	wa.Hang[waChat] = true
	start := time.Now()
	_, err := m.Send(context.Background(), "wa:"+waChat, Outgoing{Text: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Send took %s", d)
	}
	if _, err := m.Send(context.Background(), "-100123", Outgoing{Text: "x"}); err != nil || tg.Count() != 1 {
		t.Errorf("tg after a hung wa: err=%v count=%d", err, tg.Count())
	}
	if NewMux("tg", nil).SendTimeout != SendTimeout {
		t.Error("default SendTimeout")
	}
}

func TestMuxDownFailsFast(t *testing.T) {
	m, tg, wa := newMux()
	m.SendTimeout = time.Minute
	wa.Statuses = make(chan Status)
	wa.Hang[waChat] = true // would block for the whole minute if reached
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := m.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	wa.Statuses <- Status{Up: false, Reason: "logged out"}
	if st := recvStatus(t, m.Status()); st.Messenger != "wa" || st.Up || st.Reason != "logged out" {
		t.Errorf("status = %+v", st)
	}
	start := time.Now()
	if _, err := m.Send(ctx, "wa:"+waChat, Outgoing{Text: "x"}); !errors.Is(err, ErrDown) {
		t.Errorf("Send err = %v", err)
	}
	if err := m.Ack(ctx, Callback{ID: "wa:1"}, ""); !errors.Is(err, ErrDown) {
		t.Errorf("Ack err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("fail-fast took %s", d)
	}
	if _, err := m.Send(ctx, "-100123", Outgoing{Text: "x"}); err != nil || tg.Count() != 1 {
		t.Errorf("tg while wa is down: err=%v count=%d", err, tg.Count())
	}
	wa.Statuses <- Status{Up: true}
	if st := recvStatus(t, m.Status()); !st.Up {
		t.Errorf("status = %+v", st)
	}
	wa.Hang[waChat] = false
	if _, err := m.Send(ctx, "wa:"+waChat, Outgoing{Text: "x"}); err != nil || wa.Count() != 1 {
		t.Errorf("wa back up: err=%v count=%d", err, wa.Count())
	}
}

func TestMuxStatusDoesNotBlockChild(t *testing.T) {
	m, _, wa := newMux()
	wa.Statuses = make(chan Status)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := m.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	// Nobody reads m.Status(): every send must still be taken at once.
	for i := 0; i < 100; i++ {
		select {
		case wa.Statuses <- Status{Up: i%2 == 1, Reason: fmt.Sprint(i)}:
		case <-time.After(2 * time.Second):
			t.Fatalf("child blocked on status %d", i)
		}
	}
	// Older statuses are dropped: at most the one emit was holding and the
	// one stored while the last was still in flight come before the latest.
	for reads := 1; ; reads++ {
		st := recvStatus(t, m.Status())
		if st.Reason == "99" {
			break
		}
		if reads >= 3 {
			t.Fatalf("stale status %+v after %d reads", st, reads)
		}
	}
}

func TestMuxStatusTwoMessengersDown(t *testing.T) {
	m, tg, wa := newMux()
	tg.Statuses, wa.Statuses = make(chan Status), make(chan Status)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := m.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	wa.Statuses <- Status{Reason: "wa gone"}
	tg.Statuses <- Status{Reason: "tg gone"}
	got := map[string]string{}
	for len(got) < 2 {
		st := recvStatus(t, m.Status())
		got[st.Messenger] = st.Reason
	}
	if got["wa"] != "wa gone" || got["tg"] != "tg gone" {
		t.Errorf("statuses = %v", got)
	}
}

func recv(t *testing.T, in <-chan Incoming) Incoming {
	t.Helper()
	select {
	case msg, ok := <-in:
		if !ok {
			t.Fatal("merged channel closed early")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}
	return Incoming{}
}

func recvStatus(t *testing.T, ch <-chan Status) Status {
	t.Helper()
	select {
	case st := <-ch:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("no status")
	}
	return Status{}
}
