package core

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// ctxClient refuses a reaction on a context that has already ended, as the
// real API would.
type ctxClient struct{ *gh.Fake }

func (c ctxClient) React(ctx context.Context, commentID int64, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Fake.React(ctx, commentID, content)
}

// syncBuffer is a log the Run goroutine writes while the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func reactions(t *testing.T, f *gh.Fake, id int64) int {
	t.Helper()
	rs, err := f.Reactions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return len(rs)
}

func TestForwardHangStillReactsAndDoesNotRepeat(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h := newHarness(t, is)
	h.core.Clients = func(gh.Repo) (gh.Client, error) { return ctxClient{h.f}, nil }
	h.core.Config.Allow = append(h.core.Config.Allow, Principal{Chat: "200", User: user})
	h.tr.Hang["200"] = true
	h.core.SendTimeout = 100 * time.Millisecond
	start := time.Now()
	if err := h.core.Poll(context.Background()); err != nil {
		t.Fatalf("a hung chat must not fail the poll: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("poll took %s with SendTimeout %s", d, h.core.SendTimeout)
	}
	if h.tr.Count() != 1 || h.tr.Last().Chat != chat {
		t.Errorf("sent=%+v", h.tr.Sent)
	}
	if reactions(t, h.f, 101) != 1 {
		t.Error("the reaction needs its own context, not what the hung send left")
	}
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != 1 {
		t.Errorf("the healthy chat got a repeat: %d messages", h.tr.Count())
	}
}

func TestHandleHangBoundedByMux(t *testing.T) {
	h, wa, mux := whatsAppHarness(t)
	mux.SendTimeout, h.core.SendTimeout = 100*time.Millisecond, 100*time.Millisecond
	wa.Hang[waJID] = true
	start := time.Now()
	// A stray private message gets a hint, which goes to the hung chat.
	err := h.core.Handle(context.Background(), transport.Incoming{Chat: "wa:" + waJID, User: waJID, Private: true, Text: "hello"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Handle took %s", d)
	}
}

func TestRunServesTelegramWhileWhatsAppHangs(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h, wa, mux := whatsAppHarness(t, is)
	const timeout = 300 * time.Millisecond
	mux.SendTimeout, h.core.SendTimeout = timeout, timeout
	wa.Hang[waJID] = true
	h.core.Config.Poll = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.core.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	// The first poll is stuck on the WhatsApp chat; a Telegram message
	// waits at most one SendTimeout.
	start := time.Now()
	h.tr.Push(msg("hello"))
	for h.tr.Count() < 2 {
		if time.Since(start) > timeout+2*time.Second {
			t.Fatalf("telegram got %d message(s) after %s", h.tr.Count(), time.Since(start))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s := h.tr.Since(0); s[0].Chat != chat || !strings.Contains(s[0].Msg.Text, "#issuecomment-101") || !strings.Contains(s[1].Msg.Text, "Напишіть") {
		t.Errorf("telegram got %+v", s)
	}
	if reactions(t, h.f, 101) != 1 {
		t.Error("forward to telegram must still react")
	}
	if wa.Count() != 0 {
		t.Errorf("wa sent %d", wa.Count())
	}
}

func TestRunLogsTransportDown(t *testing.T) {
	h, wa, mux := whatsAppHarness(t)
	mux.SendTimeout, h.core.SendTimeout = time.Second, time.Second
	wa.Statuses = make(chan transport.Status)
	log := &syncBuffer{}
	h.core.Log = log
	h.core.Config.Poll = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.core.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	wa.Statuses <- transport.Status{Up: false, Reason: "logged out"}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(log.String(), "relay bot: transport wa down: logged out\n") {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Fail-fast now: no wait, nothing sent, Telegram unaffected.
	if _, err := h.core.Transport.Send(ctx, transport.ChatID("wa:"+waJID), transport.Outgoing{Text: "x"}); !errors.Is(err, transport.ErrDown) {
		t.Errorf("err = %v", err)
	}
	if h.tr.Count() != 0 || wa.Count() != 0 {
		t.Errorf("tg=%d wa=%d", h.tr.Count(), wa.Count())
	}
}
