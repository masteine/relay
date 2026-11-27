package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// MaxText is the longest message the core sends to Telegram, counted in
// UTF-16 units (an emoji is two). Other chats say theirs in transport.Caps.
const MaxText = 4096

var markerLine = regexp.MustCompile(`(?m)^\s*<!--\s*relay-[a-z]+\s+\{.*?\}\s*-->\s*$\n?`)

// TextLen is a string's length in UTF-16 units (the unit Telegram counts)
// or, with inUTF16 false, in runes.
func TextLen(s string, inUTF16 bool) int {
	if !inUTF16 {
		return utf8.RuneCountInString(s)
	}
	return len(utf16.Encode([]rune(s)))
}

// Truncate cuts s to at most n units of TextLen, ending with an ellipsis.
func Truncate(s string, n int, inUTF16 bool) string {
	if TextLen(s, inUTF16) <= n {
		return s
	}
	r := []rune(s)
	units := 0
	for i, ch := range r {
		w := 1
		if inUTF16 && ch > 0xFFFF {
			w = 2
		}
		if units+w > n-1 {
			return string(r[:i]) + "…"
		}
		units += w
	}
	return s
}

// Format renders a Relay comment for a chat with the given capabilities.
// The first line carries "owner/repo#n" so that a reply can be routed back,
// and the button matches the stage: ticket for a discussion, approve while
// waiting, retry after an error. A chat without buttons gets the command to
// reply with instead, on a line before the link.
func Format(alias string, repo gh.Repo, issue gh.Issue, cm protocol.Comment, labels protocol.Labels, caps transport.Caps) transport.Outgoing {
	stage, _ := protocol.ParseHeading(cm.Body)
	var cmd protocol.Command
	var button transport.Button
	data := func(c string) string { return fmt.Sprintf("%s:%s:%d", c, alias, issue.Number) }
	switch stage {
	case protocol.StageDiscuss:
		cmd, button = protocol.CmdTicket, transport.Button{Label: "📝 Сформулювати тікет", Data: data("t")}
	case protocol.StagePlan:
		if labels.NeedsApproval {
			cmd, button = protocol.CmdApprove, transport.Button{Label: "✅ Підтвердити", Data: data("a")}
		}
	case protocol.StageError:
		if e, ok, _ := protocol.ParseError(cm.Body); ok && e.Stage != protocol.StageDiscuss && e.Stage != protocol.StageApprove {
			cmd, button = protocol.CmdRetry, transport.Button{Label: "🔁 Повторити", Data: data("r")}
		}
	}
	body := strings.TrimSpace(markerLine.ReplaceAllString(cm.Body, ""))
	head := fmt.Sprintf("%s · %s#%d · %s", alias, repo, issue.Number, strings.TrimSpace(issue.Title))
	tail := fmt.Sprintf("%s#issuecomment-%d", issueURL(repo, issue), cm.ID)
	if cmd != "" && caps.Buttons == 0 {
		tail = "↩️ Відповідайте на це повідомлення: /" + string(cmd) + "\n" + tail
	}
	maxText := caps.MaxText
	if maxText <= 0 {
		maxText = MaxText
	}
	budget := maxText - TextLen(head, caps.UTF16) - TextLen(tail, caps.UTF16) - 4
	body = Truncate(body, budget, caps.UTF16)
	out := transport.Outgoing{Text: head + "\n\n" + body + "\n\n" + tail}
	if cmd != "" && caps.Buttons > 0 {
		out.Buttons = []transport.Button{button}
	}
	return out
}

func issueURL(repo gh.Repo, issue gh.Issue) string {
	if issue.URL != "" {
		return issue.URL
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number)
}
