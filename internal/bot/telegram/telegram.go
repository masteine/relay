// Package telegram is the Telegram adapter: long polling, inline buttons,
// no inbound port. It maps Telegram updates to transport.Incoming and
// transport.Outgoing to sendMessage, and nothing more.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/masteine/relay/internal/bot/transport"
)

// Telegram is a transport.Transport over the Bot API.
type Telegram struct {
	b     *bot.Bot
	botID int64
	in    chan transport.Incoming
}

// New connects with the token. Updates are handled one at a time, in
// order, so that two quick replies reach GitHub in the order they were
// written. Polling starts with Receive.
func New(token string) (*Telegram, error) {
	t := &Telegram{in: make(chan transport.Incoming)}
	b, err := bot.New(token, bot.WithDefaultHandler(t.handle), bot.WithNotAsyncHandlers())
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	t.b = b
	return t, nil
}

// Me returns the bot's username, as a startup check of the token, and
// remembers the bot's id so that only replies to the bot's own messages are
// routed.
func (t *Telegram) Me(ctx context.Context) (string, error) {
	u, err := t.b.GetMe(ctx)
	if err != nil {
		return "", err
	}
	t.botID = u.ID
	return u.Username, nil
}

func (t *Telegram) handle(ctx context.Context, _ *bot.Bot, u *models.Update) {
	in, ok := fromUpdate(u, t.botID)
	if !ok {
		return
	}
	select {
	case t.in <- in:
	case <-ctx.Done():
	}
}

// FromUpdate maps a Telegram update to an Incoming. botID, when known,
// limits ReplyTo to replies to the bot's own messages. Messages without
// text and updates the bot does not use are dropped.
func FromUpdate(u *models.Update, botID int64) (transport.Incoming, bool) {
	return fromUpdate(u, botID)
}

func fromUpdate(u *models.Update, botID int64) (transport.Incoming, bool) {
	switch {
	case u == nil:
		return transport.Incoming{}, false
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		in := transport.Incoming{User: strconv.FormatInt(cq.From.ID, 10), Callback: &transport.Callback{ID: cq.ID, Data: cq.Data}}
		switch {
		case cq.Message.Message != nil:
			in.Chat = chatID(cq.Message.Message.Chat.ID)
			in.Private = cq.Message.Message.Chat.Type == "private"
		case cq.Message.InaccessibleMessage != nil:
			in.Chat = chatID(cq.Message.InaccessibleMessage.Chat.ID)
			in.Private = cq.Message.InaccessibleMessage.Chat.Type == "private"
		}
		return in, in.Chat != ""
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		m := u.Message
		in := transport.Incoming{Chat: chatID(m.Chat.ID), Private: m.Chat.Type == "private", Text: m.Text}
		if m.Date > 0 {
			in.At = time.Unix(int64(m.Date), 0)
		}
		if m.From != nil {
			in.User = strconv.FormatInt(m.From.ID, 10)
			in.Name = displayName(m.From)
		}
		if r := m.ReplyToMessage; r != nil && (botID == 0 || (r.From != nil && r.From.ID == botID)) {
			in.ReplyTo = r.Text
		}
		return in, true
	}
	return transport.Incoming{}, false
}

func chatID(id int64) transport.ChatID { return transport.ChatID(strconv.FormatInt(id, 10)) }

func displayName(u *models.User) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

// Receive starts long polling and returns the inbox; it closes once polling
// has stopped, after ctx ends. Updates fetched but not yet handled at that
// moment are dropped, and Telegram does not resend them: the owner sees no
// confirmation and writes again.
func (t *Telegram) Receive(ctx context.Context) (<-chan transport.Incoming, error) {
	go func() {
		t.b.Start(ctx)
		close(t.in)
	}()
	return t.in, nil
}

// Send posts a plain-text message with up to four buttons in one row.
func (t *Telegram) Send(ctx context.Context, chat transport.ChatID, msg transport.Outgoing) (transport.MsgRef, error) {
	params, err := Params(chat, msg)
	if err != nil {
		return "", err
	}
	m, err := t.b.SendMessage(ctx, params)
	if err != nil {
		return "", fmt.Errorf("telegram: send: %w", err)
	}
	return transport.MsgRef(strconv.Itoa(m.ID)), nil
}

// Params builds the sendMessage request: text cut to 4096 UTF-16 units,
// buttons in one row, callback data within 64 bytes.
func Params(chat transport.ChatID, msg transport.Outgoing) (*bot.SendMessageParams, error) {
	id, err := strconv.ParseInt(string(chat), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("telegram: chat id %q", chat)
	}
	text := msg.Text
	if r := []rune(text); len(utf16.Encode(r)) > 4096 {
		units := 0
		for i, ch := range r {
			w := 1
			if ch > 0xFFFF {
				w = 2
			}
			if units+w > 4095 {
				text = string(r[:i]) + "…"
				break
			}
			units += w
		}
	}
	params := &bot.SendMessageParams{ChatID: id, Text: text}
	if len(msg.Buttons) > 0 {
		var row []models.InlineKeyboardButton
		for i, b := range msg.Buttons {
			if i >= transport.MaxButtons {
				break
			}
			if len(b.Data) > transport.MaxCallbackData {
				return nil, fmt.Errorf("telegram: callback data %q exceeds %d bytes", b.Data, transport.MaxCallbackData)
			}
			row = append(row, models.InlineKeyboardButton{Text: b.Label, CallbackData: b.Data})
		}
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
	}
	return params, nil
}

// Ack answers a callback query so that the button stops spinning.
func (t *Telegram) Ack(ctx context.Context, cb transport.Callback, text string) error {
	_, err := t.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cb.ID, Text: text})
	if err != nil {
		return fmt.Errorf("telegram: answer callback: %w", err)
	}
	return nil
}

// Token finds the bot token: RELAY_TELEGRAM_TOKEN, then the Keychain item
// (service name from the config), then the token file, which must not be
// readable by others. The token is never written to logs.
func Token(keychainService, tokenFile string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("RELAY_TELEGRAM_TOKEN")); v != "" {
		return v, nil
	}
	if keychainService != "" {
		out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
		if err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				return v, nil
			}
		}
	}
	if tokenFile != "" {
		st, err := os.Stat(tokenFile)
		if err == nil && st.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("token file %s is readable by others (mode %04o); chmod 600 it", tokenFile, st.Mode().Perm())
		}
		data, err := os.ReadFile(tokenFile)
		if err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v, nil
			}
		}
	}
	return "", errors.New("no Telegram token: set RELAY_TELEGRAM_TOKEN, store it with `relay setup`, or point telegram.token_file at it")
}
