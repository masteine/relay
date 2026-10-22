package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// MaxText is the longest message the core sends: Telegram's limit, counted
// in UTF-16 units (an emoji is two).
const MaxText = 4096

var markerLine = regexp.MustCompile(`(?m)^\s*<!--\s*relay-[a-z]+\s+\{.*?\}\s*-->\s*$\n?`)

// TextLen is a string's length in UTF-16 units, the unit Telegram counts.
func TextLen(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// Truncate cuts s to at most n UTF-16 units, ending with an ellipsis.
func Truncate(s string, n int) string {
	if TextLen(s) <= n {
		return s
	}
	r := []rune(s)
	units := 0
	for i, ch := range r {
		w := 1
		if ch > 0xFFFF {
			w = 2
		}
		if units+w > n-1 {
			return string(r[:i]) + "…"
		}
		units += w
	}
	return s
}

// Format renders a Relay comment for a chat. The first line carries
// "owner/repo#n" so that a reply can be routed back, and the buttons match
// the stage: ticket for a discussion, approve while waiting, retry after an
// error.
func Format(alias string, repo gh.Repo, issue gh.Issue, cm protocol.Comment, labels protocol.Labels) transport.Outgoing {
	stage, _ := protocol.ParseHeading(cm.Body)
	body := strings.TrimSpace(markerLine.ReplaceAllString(cm.Body, ""))
	head := fmt.Sprintf("%s · %s#%d · %s", alias, repo, issue.Number, strings.TrimSpace(issue.Title))
	link := fmt.Sprintf("%s#issuecomment-%d", issueURL(repo, issue), cm.ID)
	budget := MaxText - TextLen(head) - TextLen(link) - 4
	body = Truncate(body, budget)
	out := transport.Outgoing{Text: head + "\n\n" + body + "\n\n" + link}
	data := func(cmd string) string { return fmt.Sprintf("%s:%s:%d", cmd, alias, issue.Number) }
	switch stage {
	case protocol.StageDiscuss:
		out.Buttons = []transport.Button{{Label: "📝 Сформулювати тікет", Data: data("t")}}
	case protocol.StagePlan:
		if labels.NeedsApproval {
			out.Buttons = []transport.Button{{Label: "✅ Підтвердити", Data: data("a")}}
		}
	case protocol.StageError:
		if e, ok, _ := protocol.ParseError(cm.Body); ok && e.Stage != protocol.StageDiscuss && e.Stage != protocol.StageApprove {
			out.Buttons = []transport.Button{{Label: "🔁 Повторити", Data: data("r")}}
		}
	}
	return out
}

func issueURL(repo gh.Repo, issue gh.Issue) string {
	if issue.URL != "" {
		return issue.URL
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number)
}
