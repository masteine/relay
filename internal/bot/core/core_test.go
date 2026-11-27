package core

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

const (
	chat  = "100"
	user  = "7"
	other = "999"
)

func testConfig() Config {
	c := Defaults()
	c.Aliases = map[string]string{"todo": "masteine/relay-sandbox"}
	c.Allow = []Principal{{Chat: chat, User: user}}
	return c
}

type harness struct {
	core  *Core
	tr    *transport.Fake
	f     *gh.Fake
	clock time.Time
}

func newHarness(t *testing.T, issues ...gh.Issue) *harness {
	t.Helper()
	h := &harness{tr: transport.NewFake(), f: gh.NewFake(issues...), clock: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	h.f.CommentAs = h.f.Me // the bot posts with the owner's token
	h.core = New(testConfig(), h.tr, func(repo gh.Repo) (gh.Client, error) { return h.f, nil })
	h.core.Now = func() time.Time { return h.clock }
	h.core.Log = io.Discard
	return h
}

// msg is a message from the allowed user in a private chat.
func msg(text string) transport.Incoming {
	return transport.Incoming{Chat: chat, User: user, Private: true, Text: text}
}

func relayComment(t *testing.T, id int64, stage protocol.Stage, extra ...any) gh.Comment {
	t.Helper()
	c := protocol.StageComment{Stage: stage, Body: "Текст відповіді", Ack: protocol.Ack{Upto: 1}}
	for _, e := range extra {
		if em, ok := e.(*protocol.ErrorMarker); ok {
			c.Error = em
		}
	}
	body, err := protocol.FormatStageComment(c)
	if err != nil {
		t.Fatal(err)
	}
	return gh.Comment{ID: id, Author: protocol.BotLogin, Body: body}
}

func TestConfig(t *testing.T) {
	c, err := ParseConfig([]byte("aliases: {Todo: masteine/relay-sandbox}\nallow:\n  - {chat: -100123, user: \"7\"}\npoll: 30s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Poll != 30*time.Second || c.Lookback != 24*time.Hour || c.Telegram.Keychain != "relay-telegram" || c.Workflow != "relay.yml" {
		t.Errorf("defaults: %+v", c)
	}
	if !c.Allowed("-100123", "7") || c.Allowed("-100123", other) || c.Allowed(other, "7") {
		t.Error("Allowed")
	}
	for _, a := range []string{"todo", "Todo", "TODO"} {
		if r, ok := c.Repo(a); !ok || r.String() != "masteine/relay-sandbox" {
			t.Errorf("Repo(%q)", a)
		}
	}
	if a, ok := c.Alias(gh.Repo{Owner: "Masteine", Name: "relay-sandbox"}); !ok || a != "todo" {
		t.Error("Alias is case-insensitive and lowercase")
	}
	for _, bad := range []string{
		"allow: [{chat: a, user: b}]\n",                                                  // no aliases
		"aliases: {todo: masteine/relay-sandbox}\n",                                      // no allow
		"aliases: {todo: nope}\nallow: [{chat: a, user: b}]\n",                           // bad repo
		"aliases: {'bad alias': a/b}\nallow: [{chat: a, user: b}]\n",                     // bad alias
		"aliases: {todo: a/b, TODO: a/c}\nallow: [{chat: a, user: b}]\n",                 // case duplicate
		"aliases: {" + strings.Repeat("x", 33) + ": a/b}\nallow: [{chat: a, user: b}]\n", // too long
		"aliases: {todo: a/b}\nallow: [{chat: a}]\n",                                     // missing user
		"aliases: {todo: a/b}\nallow: [{chat: a, user: b}]\npoll: 1s\n",                  // poll too short
		"aliases: {todo: a/b}\nallow: [{chat: a, user: b}]\nunknown: 1\n",                // unknown field
	} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

func TestCreateIssueFromAlias(t *testing.T) {
	h := newHarness(t)
	for _, text := range []string{"todo: Додати темну тему\n\nз перемикачем у налаштуваннях", "Todo: друга"} {
		if err := h.core.Handle(context.Background(), msg(text)); err != nil {
			t.Fatal(err)
		}
	}
	is, err := h.f.Issue(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if is.Title != "Додати темну тему" || len(is.Labels) != 1 || is.Labels[0] != "relay:discuss" || !strings.Contains(is.Body, "Чернетка") {
		t.Errorf("issue = %+v", is)
	}
	if len(is.Comments) != 1 || is.Comments[0].Body != "Додати темну тему\n\nз перемикачем у налаштуваннях" || is.Comments[0].Author != "masteine" {
		t.Errorf("first comment must be the original text by the owner: %+v", is.Comments)
	}
	if is2, _ := h.f.Issue(context.Background(), 2); is2.Title != "друга" {
		t.Errorf("capitalised alias: %+v", is2)
	}
	sent := h.tr.Sent[0]
	if sent.Chat != chat || !strings.Contains(sent.Msg.Text, "masteine/relay-sandbox#1") || !strings.Contains(sent.Msg.Text, is.URL) {
		t.Errorf("confirmation = %+v", sent)
	}
	long := strings.Repeat("слово ", 30)
	_ = h.core.Handle(context.Background(), msg("todo: "+long))
	is3, _ := h.f.Issue(context.Background(), 3)
	if len([]rune(is3.Title)) > 80 || !strings.HasSuffix(is3.Title, "…") {
		t.Errorf("title = %q", is3.Title)
	}
}

func TestHintsOnlyInPrivateChats(t *testing.T) {
	h := newHarness(t)
	_ = h.core.Handle(context.Background(), msg("shop: buy milk"))
	if !strings.Contains(h.tr.Last().Msg.Text, "Не знаю аліаса «shop»") || !strings.Contains(h.tr.Last().Msg.Text, "todo") {
		t.Errorf("hint = %q", h.tr.Last().Msg.Text)
	}
	_ = h.core.Handle(context.Background(), msg("hello"))
	if !strings.Contains(h.tr.Last().Msg.Text, "«<аліас>: задача»") {
		t.Errorf("hint = %q", h.tr.Last().Msg.Text)
	}
	n := h.tr.Count()
	group := transport.Incoming{Chat: chat, User: user, Private: false, Text: "hello everyone"}
	_ = h.core.Handle(context.Background(), group)
	if h.tr.Count() != n {
		t.Error("no hints in a group")
	}
	if len(h.f.Log) != 0 {
		t.Errorf("nothing must reach GitHub: %v", h.f.Log)
	}
}

func TestNotAllowedIsDropped(t *testing.T) {
	h := newHarness(t)
	for _, in := range []transport.Incoming{
		{Chat: other, User: user, Private: true, Text: "todo: x"},
		{Chat: chat, User: other, Private: true, Text: "todo: x"},
		{Chat: chat, User: other, Callback: &transport.Callback{ID: "1", Data: "a:todo:1"}},
	} {
		if err := h.core.Handle(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if h.tr.Count() != 0 || len(h.f.Log) != 0 || len(h.tr.Acks) != 0 {
		t.Errorf("sent=%d log=%v acks=%d", h.tr.Count(), h.f.Log, len(h.tr.Acks))
	}
}

func TestReplyRoutesToIssue(t *testing.T) {
	h := newHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}})
	forwarded := "todo · Masteine/relay-sandbox#2 · T\n\nТекст\n\nhttps://github.com/..."
	in := msg("так, і іконку")
	in.ReplyTo = forwarded
	if err := h.core.Handle(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if h.f.LastComment(2) != "так, і іконку" {
		t.Errorf("comment = %q", h.f.LastComment(2))
	}
	if !strings.Contains(h.tr.Last().Msg.Text, "📨 masteine/relay-sandbox#2") {
		t.Errorf("confirmation = %q", h.tr.Last().Msg.Text)
	}
	if _, ok := h.core.waiting["masteine/relay-sandbox#2"]; !ok {
		t.Error("a differently cased repo in the reply must still map to the configured key")
	}
	in = msg("hm")
	in.ReplyTo = "some other message"
	_ = h.core.Handle(context.Background(), in)
	in = msg("x")
	in.ReplyTo = "other/repo#9"
	_ = h.core.Handle(context.Background(), in)
	if len(h.f.Log) != 1 {
		t.Errorf("only the first reply may post: %v", h.f.Log)
	}
}

func TestButtonPostsCommand(t *testing.T) {
	h := newHarness(t, gh.Issue{Number: 2, Labels: []string{"relay", "relay:plan", "needs-approval"}})
	cb := &transport.Callback{ID: "cb1", Data: "a:todo:2"}
	if err := h.core.Handle(context.Background(), transport.Incoming{Chat: chat, User: user, Callback: cb}); err != nil {
		t.Fatal(err)
	}
	if h.f.LastComment(2) != "/approve" {
		t.Errorf("comment = %q", h.f.LastComment(2))
	}
	if len(h.tr.Acks) != 1 || h.tr.Acks[0].ID != "cb1" {
		t.Errorf("acks = %+v", h.tr.Acks)
	}
	for _, bad := range []string{"x:todo:2", "a:nope:2", "garbage", "a:todo:0", "a:todo:99999999999999999999"} {
		_ = h.core.Handle(context.Background(), transport.Incoming{Chat: chat, User: user, Callback: &transport.Callback{ID: "cb2", Data: bad}})
	}
	if len(h.f.Log) != 1 || len(h.tr.Acks) != 6 {
		t.Errorf("bad buttons must only be acked: log=%v acks=%d", h.f.Log, len(h.tr.Acks))
	}
}

func TestPollForwardsOnceAcrossRestarts(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "Темна тема", URL: "https://github.com/masteine/relay-sandbox/issues/2", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{
		{ID: 100, Author: "masteine", Body: "todo"},
		{ID: 101, Author: "masteine", Body: "## Relay · discuss\n\nnot from the workflow\n"}, // owner text with a Relay heading
		relayComment(t, 102, protocol.StageDiscuss),
	}}
	h := newHarness(t, is)
	if err := h.core.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.tr.Count() != 1 {
		t.Fatalf("sent %d messages", h.tr.Count())
	}
	sent := h.tr.Last()
	if sent.Chat != chat || !strings.HasPrefix(sent.Msg.Text, "todo · masteine/relay-sandbox#2 · Темна тема\n") ||
		!strings.Contains(sent.Msg.Text, "Текст відповіді") || strings.Contains(sent.Msg.Text, "<!--") ||
		!strings.Contains(sent.Msg.Text, "issues/2#issuecomment-102") {
		t.Errorf("text = %q", sent.Msg.Text)
	}
	if len(sent.Msg.Buttons) != 1 || sent.Msg.Buttons[0].Data != "t:todo:2" {
		t.Errorf("buttons = %+v", sent.Msg.Buttons)
	}
	if rs := h.f.Reacts[102]; len(rs) != 1 || rs[0].Content != gh.ReactionEyes {
		t.Errorf("reaction = %+v", rs)
	}
	if h.core.lastPoll["masteine/relay-sandbox"].IsZero() {
		t.Error("a successful poll advances lastPoll")
	}
	_ = h.core.Poll(context.Background())
	restarted := New(testConfig(), h.tr, func(gh.Repo) (gh.Client, error) { return h.f, nil })
	restarted.Now, restarted.Log = h.core.Now, io.Discard
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.tr.Count() != 1 {
		t.Errorf("forwarded again after restart: %d messages", h.tr.Count())
	}
	// Two new comments in one poll: both forwarded, oldest first.
	h.f.Issues[2].Comments = append(h.f.Issues[2].Comments, relayComment(t, 104, protocol.StagePlan), relayComment(t, 103, protocol.StageTriage))
	_ = restarted.Poll(context.Background())
	if h.tr.Count() != 3 || !strings.Contains(h.tr.Sent[1].Msg.Text, "issuecomment-103") || !strings.Contains(h.tr.Sent[2].Msg.Text, "issuecomment-104") {
		t.Errorf("order: %d messages, %q / %q", h.tr.Count(), h.tr.Sent[1].Msg.Text, h.tr.Sent[2].Msg.Text)
	}
}

func TestPollRecoversFromFailures(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h := newHarness(t, is, gh.Issue{Number: 3, Title: "U", Labels: []string{"relay", "relay:plan"}, Comments: []gh.Comment{relayComment(t, 201, protocol.StageTriage)}})
	// The Issue fetch fails: nothing forwarded, lastPoll not advanced.
	h.f.FailOn["Issue"] = errors.New("502")
	if err := h.core.Poll(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if h.tr.Count() != 0 || !h.core.lastPoll["masteine/relay-sandbox"].IsZero() {
		t.Errorf("sent=%d lastPoll=%v", h.tr.Count(), h.core.lastPoll)
	}
	delete(h.f.FailOn, "Issue")
	if err := h.core.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.tr.Count() != 2 {
		t.Errorf("sent=%d after recovery", h.tr.Count())
	}
}

func TestOneBrokenChatDoesNotBlockForwarding(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h := newHarness(t, is)
	h.core.Config.Allow = append(h.core.Config.Allow, Principal{Chat: "200", User: user})
	h.tr.FailChat["200"] = errors.New("bot was kicked")
	if err := h.core.Poll(context.Background()); err != nil {
		t.Fatalf("one bad chat must not fail the poll: %v", err)
	}
	if h.tr.Count() != 1 || h.tr.Last().Chat != chat {
		t.Errorf("sent=%+v", h.tr.Sent)
	}
	if len(h.f.Reacts[101]) != 1 {
		t.Error("reaction must follow a partial delivery")
	}
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != 1 {
		t.Error("must not repeat for the good chat")
	}
	// Every chat failing: no reaction, tried again next time.
	h.f.Issues[2].Comments = append(h.f.Issues[2].Comments, relayComment(t, 102, protocol.StageTriage))
	h.tr.FailSend = errors.New("network")
	if err := h.core.Poll(context.Background()); err == nil || len(h.f.Reacts[102]) != 0 {
		t.Errorf("err=%v reacts=%v", err, h.f.Reacts[102])
	}
	h.tr.FailSend = nil
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != 2 || len(h.f.Reacts[102]) != 1 {
		t.Errorf("count=%d reacts=%v", h.tr.Count(), h.f.Reacts[102])
	}
}

func TestForwardCompletesWhenStopping(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h := newHarness(t, is)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled context: the listing may fail in real life, but once a
	// forward has started, the send and the reaction use a detached context.
	_ = h.core.forward(ctx, h.f, "todo", gh.Repo{Owner: "masteine", Name: "relay-sandbox"}, is)
	if h.tr.Count() != 1 || len(h.f.Reacts[101]) != 1 {
		t.Errorf("sent=%d reacts=%v", h.tr.Count(), h.f.Reacts[101])
	}
}

func TestButtonsByStage(t *testing.T) {
	repo := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	cases := []struct {
		labels []string
		cm     gh.Comment
		want   string
	}{
		{[]string{"relay:discuss"}, relayComment(t, 1, protocol.StageDiscuss), "t:todo:5"},
		{[]string{"relay", "relay:plan", "needs-approval"}, relayComment(t, 2, protocol.StagePlan), "a:todo:5"},
		{[]string{"relay", "relay:plan"}, relayComment(t, 3, protocol.StagePlan), ""},
		{[]string{"relay", "relay:failed"}, relayComment(t, 4, protocol.StageError, &protocol.ErrorMarker{Stage: protocol.StageTriage}), "r:todo:5"},
		{[]string{"relay:discuss"}, relayComment(t, 5, protocol.StageError, &protocol.ErrorMarker{Stage: protocol.StageDiscuss}), ""},
		{[]string{"relay", "relay:triage"}, relayComment(t, 6, protocol.StageTicket), ""},
		{[]string{"relay", "relay:plan"}, relayComment(t, 7, protocol.StageIgnored), ""},
	}
	for _, tc := range cases {
		labels, _ := protocol.ParseLabels(tc.labels)
		out := Format("todo", repo, gh.Issue{Number: 5, Title: "T"}, protocol.Comment{ID: tc.cm.ID, Author: tc.cm.Author, Body: tc.cm.Body}, labels, transport.TelegramCaps)
		got := ""
		if len(out.Buttons) > 0 {
			got = out.Buttons[0].Data
		}
		if got != tc.want {
			t.Errorf("labels %v comment %d: button %q, want %q", tc.labels, tc.cm.ID, got, tc.want)
		}
		if len(out.Buttons) > transport.MaxButtons || len(got) > transport.MaxCallbackData {
			t.Errorf("button limits: %+v", out.Buttons)
		}
	}
	// The longest allowed alias with a large Issue number still fits.
	long := Format(strings.Repeat("a", MaxAliasLen), repo, gh.Issue{Number: 999999999}, protocol.Comment{ID: 1, Author: protocol.BotLogin, Body: "## Relay · discuss\n\nx"}, protocol.Labels{State: protocol.StateDiscuss}, transport.TelegramCaps)
	if len(long.Buttons[0].Data) > transport.MaxCallbackData {
		t.Errorf("callback data %d bytes", len(long.Buttons[0].Data))
	}
}

func TestFormatTruncatesInUTF16(t *testing.T) {
	repo := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	body, _ := protocol.FormatStageComment(protocol.StageComment{Stage: protocol.StagePlan, Body: strings.Repeat("😀", 3000), Ack: protocol.Ack{Upto: 1}})
	out := Format("todo", repo, gh.Issue{Number: 5, Title: "T"}, protocol.Comment{ID: 9, Author: protocol.BotLogin, Body: body}, protocol.Labels{}, transport.TelegramCaps)
	if n := TextLen(out.Text, true); n > MaxText {
		t.Errorf("text is %d UTF-16 units", n)
	}
	if !strings.HasSuffix(out.Text, "#issuecomment-9") {
		t.Error("the link must survive truncation")
	}
	if Truncate("abc", 3, true) != "abc" || Truncate("abcd", 3, true) != "ab…" {
		t.Errorf("Truncate: %q %q", Truncate("abc", 3, true), Truncate("abcd", 3, true))
	}
}

func TestSilenceNotice(t *testing.T) {
	h := newHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 50, protocol.StageDiscuss)}})
	_ = h.core.Poll(context.Background()) // forwards the old comment first
	in := msg("x")
	in.ReplyTo = "masteine/relay-sandbox#2"
	if err := h.core.Handle(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	before := h.tr.Count()
	h.clock = h.clock.Add(5 * time.Minute)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != before {
		t.Fatal("too early for a notice")
	}
	h.f.Runs = 1
	h.clock = h.clock.Add(6 * time.Minute)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != before {
		t.Fatal("a run in progress must not trigger the notice")
	}
	h.f.Runs = 0
	h.clock = h.clock.Add(11 * time.Minute)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != before+1 || !strings.Contains(h.tr.Last().Msg.Text, "⏳ todo · masteine/relay-sandbox#2") {
		t.Fatalf("count=%d last=%q", h.tr.Count(), h.tr.Last().Msg.Text)
	}
	h.clock = h.clock.Add(time.Hour)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != before+1 {
		t.Error("the notice must be sent once")
	}
}

func TestSilenceClearedOnlyByNewerRelayComment(t *testing.T) {
	// An old, not yet forwarded Relay comment (id 50) must not clear a wait
	// started by a later owner message; a newer one (id 2000) must.
	h := newHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 50, protocol.StageDiscuss)}})
	in := msg("x")
	in.ReplyTo = "masteine/relay-sandbox#2"
	_ = h.core.Handle(context.Background(), in) // posts comment 1000
	_ = h.core.Poll(context.Background())       // forwards #50
	if _, ok := h.core.waiting["masteine/relay-sandbox#2"]; !ok {
		t.Fatal("an older comment must not clear the wait")
	}
	h.f.Issues[2].Comments = append(h.f.Issues[2].Comments, relayComment(t, 2000, protocol.StageDiscuss))
	h.clock = h.clock.Add(2 * time.Minute)
	_ = h.core.Poll(context.Background())
	if _, ok := h.core.waiting["masteine/relay-sandbox#2"]; ok {
		t.Fatal("a newer comment clears the wait")
	}
	before := h.tr.Count()
	h.clock = h.clock.Add(time.Hour)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != before {
		t.Errorf("no notice expected, last=%q", h.tr.Last().Msg.Text)
	}
}

func TestSilenceNoticeWhenRunCheckFails(t *testing.T) {
	// An alias added before /relay:init: ActiveRuns 404s forever. The
	// notice still goes out once, the wait is dropped, and lastPoll moves.
	h := newHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}})
	h.f.FailOn["ActiveRuns"] = errors.New("404 workflow not found")
	in := msg("x")
	in.ReplyTo = "masteine/relay-sandbox#2"
	_ = h.core.Handle(context.Background(), in)
	h.clock = h.clock.Add(11 * time.Minute)
	err := h.core.Poll(context.Background())
	if err == nil {
		t.Fatal("the run check error is reported")
	}
	if h.tr.Count() != 2 || !strings.Contains(h.tr.Last().Msg.Text, "⏳") || !strings.Contains(h.tr.Last().Msg.Text, "/relay:init") {
		t.Fatalf("count=%d last=%q", h.tr.Count(), h.tr.Last().Msg.Text)
	}
	if h.core.lastPoll["masteine/relay-sandbox"].IsZero() {
		t.Error("a silence error must not hold lastPoll back")
	}
	if _, ok := h.core.waiting["masteine/relay-sandbox#2"]; ok {
		t.Error("the wait must be dropped")
	}
	h.clock = h.clock.Add(time.Hour)
	_ = h.core.Poll(context.Background())
	if h.tr.Count() != 2 {
		t.Error("no repeated notice")
	}
}

func TestPollWindowIsClamped(t *testing.T) {
	h := newHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}})
	// A failure that keeps recurring: the window must not grow past Lookback.
	h.f.FailOn["Issue"] = errors.New("502")
	_ = h.core.Poll(context.Background())
	h.clock = h.clock.Add(3 * 24 * time.Hour)
	_ = h.core.Poll(context.Background())
	if floor := h.clock.Add(-h.core.Config.Lookback); h.f.LastSince.Before(floor) {
		t.Errorf("since %v is before the floor %v", h.f.LastSince, floor)
	}
	// After a success the window is the last poll minus the overlap.
	delete(h.f.FailOn, "Issue")
	_ = h.core.Poll(context.Background())
	h.clock = h.clock.Add(h.core.Config.Poll)
	_ = h.core.Poll(context.Background())
	if want := h.clock.Add(-h.core.Config.Poll - time.Minute); !h.f.LastSince.Equal(want) {
		t.Errorf("since %v, want %v", h.f.LastSince, want)
	}
}

func TestRunStopsWhenTransportCloses(t *testing.T) {
	h := newHarness(t)
	h.core.Config.Poll = 5 * time.Second
	done := make(chan error, 1)
	go func() { done <- h.core.Run(context.Background()) }()
	h.tr.Push(msg("hello"))
	h.tr.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if h.tr.Count() != 1 {
		t.Errorf("sent %d", h.tr.Count())
	}
}

func TestRunStopsOnContextAndTicks(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}}
	h := newHarness(t, is)
	h.core.Config.Poll = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.core.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return on cancel")
	}
}
