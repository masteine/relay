// Package transport is the seam between the bot's core and a messenger. An
// adapter implements Transport for one messenger (Telegram in v0, Discord
// and Slack in v2) and knows nothing about GitHub; the core knows nothing
// about messengers and relies only on this lowest common denominator: text,
// up to four buttons, links.
package transport

import (
	"context"
	"errors"
	"time"
)

// ChatID identifies a conversation in the messenger's own terms (a Telegram
// chat id, a Discord thread id). The core treats it as opaque.
type ChatID string

// Incoming is a message or a button press from a chat.
type Incoming struct {
	Chat ChatID
	// User identifies who wrote it, in the messenger's own terms. The
	// allowlist is on (Chat, User) pairs.
	User string
	// Name is the sender's display name, for humans only.
	Name string
	// Private is true for a one-to-one chat with the bot. Hints are only
	// sent there; in a group every stray message would get one.
	Private bool
	Text    string
	// ReplyTo is the text of the bot's message this one replies to, "" when
	// it is not a reply or replies to someone else's message. The core
	// routes replies by the "owner/repo#n" the forwarded message carries.
	ReplyTo string
	// Callback is set for a button press; Text is empty then.
	Callback *Callback
	// At is when the messenger says the message was sent; zero when unknown.
	At time.Time
}

// Callback is a pressed button. Data is what the core put into the button;
// ID is what the adapter needs to acknowledge the press.
type Callback struct {
	ID   string
	Data string
}

// Button is one inline button under a message.
type Button struct {
	Label string
	// Data comes back in Callback.Data; adapters may limit its length
	// (Telegram: 64 bytes).
	Data string
}

// Outgoing is a message to send.
type Outgoing struct {
	Text    string
	Buttons []Button
}

// MsgRef identifies a sent message, for adapters that can edit or reply.
type MsgRef string

// Transport is what the core needs from a messenger.
type Transport interface {
	// Receive returns a channel of everything addressed to the bot, in the
	// order it arrived. The adapter closes it after ctx ends.
	Receive(ctx context.Context) (<-chan Incoming, error)
	// Send delivers a message with up to four buttons.
	Send(ctx context.Context, chat ChatID, msg Outgoing) (MsgRef, error)
	// Ack confirms a button press (Telegram's answerCallbackQuery); text,
	// when not empty, is shown to the user as a short notice.
	Ack(ctx context.Context, cb Callback, text string) error
}

// MaxButtons is the most buttons the core puts under one message.
const MaxButtons = 4

// MaxCallbackData is the longest button data an adapter must accept.
const MaxCallbackData = 64

// SendTimeout bounds one Send through a Mux, so that a messenger that hangs
// cannot hold up the core or the other messengers.
const SendTimeout = 8 * time.Second

// ErrDown is what a Mux returns at once for a chat whose messenger last
// reported itself down, without touching the network.
var ErrDown = errors.New("transport: messenger is down")

// Caps are what a chat can display: how many buttons go under a message
// (0: none, the core asks for a reply instead), the longest text, and
// whether that length is counted in UTF-16 units (Telegram) or in runes.
type Caps struct {
	Buttons int
	MaxText int
	UTF16   bool
}

// TelegramCaps are the defaults for a transport that is not Capable.
var TelegramCaps = Caps{Buttons: MaxButtons, MaxText: 4096, UTF16: true}

// Capable is implemented by a transport whose chats differ from
// TelegramCaps.
type Capable interface {
	Caps(chat ChatID) Caps
}

// Status is a messenger's connection state. Reason is for humans ("logged
// out", "connect failure"). Notify asks the core to tell the owner even when
// this is the first status after start (a logout found on start); otherwise
// the first status is only logged.
type Status struct {
	Messenger string
	Up        bool
	Reason    string
	Notify    bool
}

// Reporter is implemented by a transport that reports its connection
// state. The channel lives as long as the transport's Receive context.
type Reporter interface {
	Status() <-chan Status
}
