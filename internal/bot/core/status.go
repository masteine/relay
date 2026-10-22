package core

import (
	"context"
	"fmt"

	"github.com/masteine/relay/internal/bot/transport"
)

// link is what Run remembers about one messenger's connection.
type link struct {
	seen bool // a status arrived since the start
	up   bool
	// downNotified is set while the chats of the other messengers have
	// been told this one is down and not yet that it is back.
	downNotified bool
}

// linkStatus logs a messenger's status and tells the other messengers'
// chats when it goes down and, after that, when it is back. The first
// status after a start is only logged ("not paired", a network hiccup on
// every restart), unless the adapter asks with Notify (a logout found on
// start). It runs on the Run goroutine.
func (c *Core) linkStatus(ctx context.Context, l *link, st transport.Status) {
	state := "down"
	if st.Up {
		state = "up"
	}
	if st.Reason != "" {
		state += ": " + st.Reason
	}
	c.logf("transport %s %s", st.Messenger, state)
	m, ok := MessengerKeyed(st.Messenger)
	if !ok {
		c.logf("transport %s: unknown messenger, no notice", st.Messenger)
		return
	}
	wasUp, first := l.up, !l.seen
	l.seen, l.up = true, st.Up
	switch {
	case !st.Up && !l.downNotified && (st.Notify || !first && wasUp):
		l.downNotified = true
		why := ""
		if st.Reason != "" {
			why = ": " + st.Reason
		}
		c.notice(ctx, m.Name, fmt.Sprintf("⚠️ %s відключено%s. %s", m.Display, why, m.Setup))
	case st.Up && l.downNotified:
		l.downNotified = false
		c.notice(ctx, m.Name, fmt.Sprintf("✅ %s знову на зв'язку", m.Display))
	}
}

// notice tells every chat except the messenger's own; a failure is only
// logged, since it would not be tried again anyway.
func (c *Core) notice(ctx context.Context, messenger, text string) {
	for _, chat := range c.Config.ChatsExcept(messenger) {
		if _, err := c.Transport.Send(ctx, transport.ChatID(chat), transport.Outgoing{Text: text}); err != nil {
			c.logf("%s notice to chat %s: %v", messenger, chat, err)
		}
	}
}
