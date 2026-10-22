package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/masteine/relay/internal/bot/telegram"
	"github.com/masteine/relay/internal/bot/transport"
)

// AskTokenTerminal reads the Telegram bot token from the terminal without
// echoing it. Without a terminal it reads one line from stdin.
func AskTokenTerminal(out io.Writer) func() (string, error) {
	return func() (string, error) {
		fmt.Fprint(out, "Telegram bot token from @BotFather (input is hidden): ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(out)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(b)), nil
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
}

// ConfirmTerminal asks a yes/no question on the terminal; the default is no.
func ConfirmTerminal(out io.Writer) func(prompt string) bool {
	return func(prompt string) bool {
		fmt.Fprintf(out, "%s [y/N] ", prompt)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.ToLower(strings.TrimSpace(line))
		return line == "y" || line == "yes"
	}
}

// CaptureTimeout bounds how long setup waits for the owner's first message.
const CaptureTimeout = 3 * time.Minute

// Sender is who wrote to the bot during Capture.
type Sender struct {
	Chat, User string
	Name       string // the sender's display name, for the confirm prompt
	BotName    string
}

// CheckTokenTelegram validates a token with getMe.
func CheckTokenTelegram(ctx context.Context, token string) error {
	tg, err := telegram.New(token)
	if err != nil {
		return err
	}
	_, err = tg.Me(ctx)
	return err
}

// Acceptable says whether a message may identify the owner: a private chat
// (never a group, where anyone could write), sent after the capture started
// (never the backlog Telegram keeps for a new bot).
func Acceptable(in transport.Incoming, since time.Time) bool {
	if in.Chat == "" || in.User == "" || !in.Private {
		return false
	}
	if !in.At.IsZero() && in.At.Before(since) {
		return false
	}
	return true
}

// CaptureTelegram starts the bot briefly, validates the token with getMe,
// and returns the first acceptable sender. confirm, when set, is asked
// before the sender is accepted.
func CaptureTelegram(ctx context.Context, token string, confirm func(string) bool, out io.Writer) (Sender, error) {
	ctx, cancel := context.WithTimeout(ctx, CaptureTimeout)
	defer cancel()
	tg, err := telegram.New(token)
	if err != nil {
		return Sender{}, err
	}
	name, err := tg.Me(ctx)
	if err != nil {
		return Sender{}, fmt.Errorf("the token does not work: %w", err)
	}
	if out != nil {
		fmt.Fprintf(out, "open Telegram, start a private chat with @%s and send it any message (for example /start); waiting up to %s…\n", name, CaptureTimeout)
	}
	since := time.Now().Add(-5 * time.Second)
	in, err := tg.Receive(ctx)
	if err != nil {
		return Sender{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return Sender{}, errors.New("no message arrived in time; run relay setup again and send the bot a message")
		case msg, ok := <-in:
			if !ok {
				return Sender{}, errors.New("telegram stopped")
			}
			if !Acceptable(msg, since) {
				if out != nil && msg.Chat != "" && !msg.Private {
					fmt.Fprintf(out, "ignoring a message from chat %s: use a private chat with the bot\n", msg.Chat)
				}
				continue
			}
			s := Sender{Chat: string(msg.Chat), User: msg.User, Name: msg.Name, BotName: name}
			who := s.User
			if s.Name != "" {
				// %q keeps a name with control characters from spoofing the prompt.
				who = fmt.Sprintf("%q (id %s)", s.Name, s.User)
			}
			if confirm != nil && !confirm(fmt.Sprintf("allow %s in chat %s to drive Relay?", who, s.Chat)) {
				if out != nil {
					fmt.Fprintln(out, "not allowed; waiting for another message…")
				}
				continue
			}
			return s, nil
		}
	}
}
