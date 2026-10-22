package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/masteine/relay/internal/bot/transport"
)

func TestFromUpdate(t *testing.T) {
	msg := &models.Update{Message: &models.Message{
		ID: 5, Date: 1760000000, Chat: models.Chat{ID: -100123, Type: "supergroup"}, From: &models.User{ID: 7, Username: "dave"}, Text: "todo: x",
		ReplyToMessage: &models.Message{Text: "todo · a/b#2 · T", From: &models.User{ID: 42}},
	}}
	// Bot id known: only replies to the bot's messages carry ReplyTo.
	in, ok := FromUpdate(msg, 42)
	if !ok || in.Chat != "-100123" || in.User != "7" || in.Private || in.Text != "todo: x" || in.ReplyTo != "todo · a/b#2 · T" || in.Callback != nil || in.At.Unix() != 1760000000 || in.Name != "@dave" {
		t.Errorf("message: %+v %v", in, ok)
	}
	in, _ = FromUpdate(msg, 43)
	if in.ReplyTo != "" {
		t.Errorf("a reply to someone else must not route: %+v", in)
	}
	in, _ = FromUpdate(msg, 0)
	if in.ReplyTo == "" {
		t.Errorf("unknown bot id keeps ReplyTo: %+v", in)
	}
	priv := &models.Update{Message: &models.Message{Chat: models.Chat{ID: 7, Type: "private"}, From: &models.User{ID: 7}, Text: "hi"}}
	if in, _ := FromUpdate(priv, 42); !in.Private {
		t.Errorf("private chat: %+v", in)
	}

	cb := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "cb9", From: models.User{ID: 7}, Data: "a:todo:2",
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: 100, Type: "private"}}},
	}}
	in, ok = FromUpdate(cb, 42)
	if !ok || in.Chat != "100" || in.User != "7" || !in.Private || in.Callback == nil || in.Callback.ID != "cb9" || in.Callback.Data != "a:todo:2" {
		t.Errorf("callback: %+v %v", in, ok)
	}
	inacc := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb10", From: models.User{ID: 7}, Data: "t:todo:3",
		Message: models.MaybeInaccessibleMessage{InaccessibleMessage: &models.InaccessibleMessage{Chat: models.Chat{ID: -5}}}}}
	if in, ok := FromUpdate(inacc, 42); !ok || in.Chat != "-5" {
		t.Errorf("inaccessible message: %+v %v", in, ok)
	}
	noMsg := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "cb11", From: models.User{ID: 7}, Data: "t:todo:3"}}
	if _, ok := FromUpdate(noMsg, 42); ok {
		t.Error("a callback without a chat must be dropped")
	}

	for _, drop := range []*models.Update{nil, {}, {Message: &models.Message{Chat: models.Chat{ID: 1}, Text: "   "}}} {
		if _, ok := FromUpdate(drop, 42); ok {
			t.Errorf("should drop %+v", drop)
		}
	}
}

func TestParams(t *testing.T) {
	if _, err := Params(transport.ChatID("not-a-number"), transport.Outgoing{Text: "x"}); err == nil {
		t.Error("bad chat id: want error")
	}
	p, err := Params(transport.ChatID("-100123"), transport.Outgoing{Text: "hi", Buttons: []transport.Button{{Label: "A", Data: "a:todo:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ChatID != int64(-100123) || p.Text != "hi" {
		t.Errorf("params = %+v", p)
	}
	kb, ok := p.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 || kb.InlineKeyboard[0][0].CallbackData != "a:todo:1" {
		t.Errorf("keyboard = %+v", p.ReplyMarkup)
	}
	if _, err := Params("1", transport.Outgoing{Text: "x", Buttons: []transport.Button{{Label: "A", Data: strings.Repeat("x", 65)}}}); err == nil {
		t.Error("64-byte limit: want error")
	}
	// Emoji count as two UTF-16 units: 2100 of them exceed 4096 units.
	long := strings.Repeat("😀", 2100)
	p, _ = Params("1", transport.Outgoing{Text: long})
	if n := len([]rune(p.Text)); n > 2048 || !strings.HasSuffix(p.Text, "…") {
		t.Errorf("truncated to %d runes", n)
	}
	var units int
	for _, r := range p.Text {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	if units > 4096 {
		t.Errorf("%d UTF-16 units", units)
	}
}

func TestToken(t *testing.T) {
	t.Setenv("RELAY_TELEGRAM_TOKEN", "")
	dir := t.TempDir()
	if _, err := Token("", filepath.Join(dir, "missing")); err == nil {
		t.Error("want error when nothing is configured")
	}
	f := filepath.Join(dir, "token")
	if err := os.WriteFile(f, []byte("123:abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Token("", f); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Errorf("mode 644 must be refused: %v", err)
	}
	if err := os.Chmod(f, 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := Token("", f); err != nil || tok != "123:abc" {
		t.Errorf("file: %q %v", tok, err)
	}
	t.Setenv("RELAY_TELEGRAM_TOKEN", "env-token")
	if tok, _ := Token("", f); tok != "env-token" {
		t.Errorf("env must win, got %q", tok)
	}
}
