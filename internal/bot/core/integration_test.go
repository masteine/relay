//go:build integration

package core

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"strconv"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
)

// TestIntegrationGitHub drives the core against a real repository with the
// fake transport, so that the gh.Client methods the bot relies on
// (CreateIssue, ListUpdated, Reactions, React, ActiveRuns, Login) are
// exercised for real. Run with:
//
//	RELAY_IT_REPO=masteine/relay-sandbox go test -tags integration ./internal/bot/core/ -run Integration -v
func TestIntegrationGitHub(t *testing.T) {
	full := os.Getenv("RELAY_IT_REPO")
	if full == "" {
		t.Skip("RELAY_IT_REPO not set")
	}
	repo, err := gh.ParseRepo(full)
	if err != nil {
		t.Fatal(err)
	}
	token, err := gh.Token()
	if err != nil {
		t.Skip("no GitHub token:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := Defaults()
	cfg.Aliases = map[string]string{"it": full}
	cfg.Allow = []Principal{{Chat: "c", User: "u"}}
	tr := transport.NewFake()
	c := New(cfg, tr, func(r gh.Repo) (gh.Client, error) { return gh.New(r, token) })
	priv := func(in transport.Incoming) transport.Incoming { in.Private = true; return in }
	_ = priv
	c.Log = io.Discard

	// 1. "it: text" → a draft Issue with the text as the first comment.
	text := "інтеграційний тест бота " + time.Now().UTC().Format(time.RFC3339)
	if err := c.Handle(ctx, transport.Incoming{Chat: "c", User: "u", Text: "it: " + text}); err != nil {
		t.Fatal(err)
	}
	conf := tr.Last().Msg.Text
	if !strings.Contains(conf, "✅ it · "+full+"#") {
		t.Fatalf("confirmation = %q", conf)
	}
	n := 0
	for _, f := range strings.Fields(conf) {
		if i := strings.Index(f, "#"); i >= 0 && strings.HasPrefix(f, full) {
			for _, ch := range f[i+1:] {
				if ch < '0' || ch > '9' {
					break
				}
				n = n*10 + int(ch-'0')
			}
		}
	}
	if n == 0 {
		t.Fatalf("no issue number in %q", conf)
	}
	t.Logf("created %s#%d", full, n)
	cl, _ := c.client(repo)
	issue, err := cl.Issue(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "relay:discuss" || len(issue.Comments) != 1 || issue.Comments[0].Body != text {
		t.Fatalf("issue = %+v", issue)
	}

	// 2. Poll forwards Relay comments the bot has not reacted to yet
	// (whatever the sandbox holds from earlier runs), never the owner's
	// own comments, and reacts to each one it forwarded.
	sent := tr.Count()
	if err := c.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	forwarded := tr.Sent[sent:]
	t.Logf("forwarded %d Relay comment(s) on first poll", len(forwarded))
	for _, s := range forwarded {
		if !strings.Contains(s.Msg.Text, "## Relay · ") || strings.Contains(s.Msg.Text, text) {
			t.Fatalf("unexpected forward: %q", s.Msg.Text)
		}
	}
	// A second poll (same reactions on GitHub) forwards nothing again.
	if err := c.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.Count() != sent+len(forwarded) {
		t.Fatalf("second poll re-forwarded %d", tr.Count()-sent-len(forwarded))
	}

	// 3. Reactions round trip on the first comment.
	id := issue.Comments[0].ID
	if err := cl.React(ctx, id, gh.ReactionEyes); err != nil {
		t.Fatal(err)
	}
	rs, err := cl.Reactions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	me, _ := cl.Login(ctx)
	found := false
	for _, r := range rs {
		if r.Content == gh.ReactionEyes && strings.EqualFold(r.User, me) {
			found = true
		}
	}
	if !found {
		t.Fatalf("reaction not found: %+v (me=%s)", rs, me)
	}

	// 4. ListUpdated sees the Issue; ActiveRuns answers.
	list, err := cl.ListUpdated(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, is := range list {
		if is.Number == n {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("ListUpdated did not return #%d", n)
	}
	if _, err := cl.ActiveRuns(ctx, cfg.Workflow); err != nil {
		t.Fatal(err)
	}

	// 5. A reply routes to the Issue.
	if err := c.Handle(ctx, transport.Incoming{Chat: "c", User: "u", Text: "відповідь з тесту", ReplyTo: "it · " + full + "#" + strconv.Itoa(n) + " · x"}); err != nil {
		t.Fatal(err)
	}
	issue, _ = cl.Issue(ctx, n)
	if last := issue.Comments[len(issue.Comments)-1]; last.Body != "відповідь з тесту" {
		t.Fatalf("reply not posted: %+v", last)
	}
}
