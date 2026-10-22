package core

import (
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

const waJID = "380501234567@s.whatsapp.net"

// noButtons are the capabilities of a chat like WhatsApp's.
var noButtons = transport.Caps{Buttons: 0, MaxText: 65536, UTF16: false}

// whatsAppHarness puts the harness's Fake behind a Mux as "tg" next to a
// "wa" Fake without buttons, and allows one WhatsApp chat the way the
// config writes it.
func whatsAppHarness(t *testing.T, issues ...gh.Issue) (*harness, *transport.Fake, *transport.Mux) {
	t.Helper()
	h := newHarness(t, issues...)
	wa := transport.NewFake()
	wa.CapsByChat[waJID] = noButtons
	mux := transport.NewMux(UnprefixedKey(), map[string]transport.Transport{"tg": h.tr, "wa": wa})
	h.core.Transport = mux
	h.core.Config.Allow = append(h.core.Config.Allow, Principal{Messenger: NameWhatsApp, Chat: waJID, User: waJID})
	return h, wa, mux
}

func TestConfigMessengers(t *testing.T) {
	if NormalizeMessenger("") != NameTelegram || NormalizeMessenger(" WhatsApp ") != NameWhatsApp {
		t.Error("NormalizeMessenger")
	}
	tg, _ := MessengerNamed("")
	wa, _ := MessengerKeyed("wa")
	if tg.Key != "tg" || !tg.Unprefixed || wa.Name != NameWhatsApp || wa.Unprefixed || UnprefixedKey() != "tg" {
		t.Errorf("table: %+v %+v", tg, wa)
	}
	if _, ok := MessengerNamed("signal"); ok {
		t.Error("unknown name found")
	}
	tgP := Principal{Chat: "-100123", User: "7"}
	tgExplicit := Principal{Messenger: "telegram", Chat: "-100123", User: "7"}
	waP := Principal{Messenger: NameWhatsApp, Chat: waJID, User: waJID}
	if tgP.ChatKey() != "-100123" || tgExplicit.ChatKey() != "-100123" || waP.ChatKey() != "wa:"+waJID {
		t.Errorf("ChatKey: %q %q %q", tgP.ChatKey(), tgExplicit.ChatKey(), waP.ChatKey())
	}
	c := Defaults()
	c.Allow = []Principal{tgP, tgExplicit, waP, {Chat: "55", User: "7"}}
	if got := strings.Join(c.Chats(), " "); got != "-100123 55 wa:"+waJID {
		t.Errorf("Chats = %q", got)
	}
	if got := strings.Join(c.ChatsExcept(NameWhatsApp), " "); got != "-100123 55" {
		t.Errorf("ChatsExcept(whatsapp) = %q", got)
	}
	if got := strings.Join(c.ChatsExcept(""), " "); got != "wa:"+waJID {
		t.Errorf("ChatsExcept(\"\") = %q", got)
	}
	if !c.Allowed("wa:"+waJID, waJID) || c.Allowed(waJID, waJID) || !c.Allowed("-100123", "7") {
		t.Error("Allowed compares ChatKeys")
	}
	if !c.Has("") || !c.Has("Telegram") || !c.Has(NameWhatsApp) {
		t.Error("Has")
	}
	if c.WhatsApp.Store != DefaultWhatsAppStore() || !strings.HasSuffix(c.WhatsApp.Store, "/.config/relay/whatsapp/session.db") {
		t.Errorf("default store %q", c.WhatsApp.Store)
	}
	tgOnly := Defaults()
	tgOnly.Allow = []Principal{tgP}
	if tgOnly.Has(NameWhatsApp) {
		t.Error("Has(whatsapp) on a Telegram-only config")
	}
}

func TestConfigWhatsAppValidation(t *testing.T) {
	const head = "aliases: {todo: masteine/relay-sandbox}\n"
	good := head + "allow:\n  - {chat: \"-100123\", user: \"7\"}\n  - {messenger: whatsapp, chat: \"" + waJID + "\", user: \"" + waJID + "\"}\nwhatsapp:\n  store: /tmp/x/session.db\n"
	c, err := ParseConfig([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if c.WhatsApp.Store != "/tmp/x/session.db" || !c.Allowed("wa:"+waJID, waJID) {
		t.Errorf("config = %+v", c)
	}
	if _, err := ParseConfig([]byte(head + "allow:\n  - {messenger: whatsapp, chat: \"" + waJID + "\", user: \"" + waJID + "\"}\n")); err != nil {
		t.Errorf("WhatsApp only: %v", err)
	}
	if _, err := ParseConfig([]byte(head + "allow:\n  - {messenger: telegram, chat: \"-100123\", user: \"7\"}\n")); err != nil {
		t.Errorf("explicit telegram: %v", err)
	}
	for _, bad := range []string{
		`{messenger: whatsapp, chat: "120363025@g.us", user: "120363025@g.us"}`,             // group
		`{messenger: whatsapp, chat: "12345678901234@lid", user: "12345678901234@lid"}`,     // hidden id
		`{messenger: whatsapp, chat: "380501234567", user: "380501234567"}`,                 // no @
		`{messenger: whatsapp, chat: "abc@s.whatsapp.net", user: "abc@s.whatsapp.net"}`,     // not digits
		`{messenger: whatsapp, chat: "1234@s.whatsapp.net", user: "1234@s.whatsapp.net"}`,   // too short
		`{messenger: whatsapp, chat: "` + waJID + `", user: "380509999999@s.whatsapp.net"}`, // chat != user
		`{messenger: whatsapp, chat: "` + waJID + `", user: "380501234567"}`,                // user not a JID
		`{messenger: signal, chat: "1", user: "1"}`,                                         // unknown
		`{messenger: telegram, chat: "` + waJID + `", user: "7"}`,                           // Telegram rules
		`{chat: "abc", user: "7"}`, // Telegram rules
		`{messenger: whatsapp, chat: "380501234567:12@s.whatsapp.net", user: "380501234567:12@s.whatsapp.net"}`, // device
	} {
		if _, err := ParseConfig([]byte(head + "allow:\n  - " + bad + "\n")); err == nil {
			t.Errorf("want error for %s", bad)
		}
	}
	for jid, want := range map[string]bool{waJID: true, "12345@s.whatsapp.net": true, strings.Repeat("1", 21) + "@s.whatsapp.net": false, "": false, "@s.whatsapp.net": false} {
		if ValidJID(jid) != want {
			t.Errorf("ValidJID(%q) != %v", jid, want)
		}
	}
}

func TestFormatWithoutButtons(t *testing.T) {
	repo := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	issue := gh.Issue{Number: 5, Title: "T"}
	cases := []struct {
		labels []string
		cm     gh.Comment
		want   string
	}{
		{[]string{"relay:discuss"}, relayComment(t, 1, protocol.StageDiscuss), "/ticket"},
		{[]string{"relay", "relay:plan", "needs-approval"}, relayComment(t, 2, protocol.StagePlan), "/approve"},
		{[]string{"relay", "relay:plan"}, relayComment(t, 3, protocol.StagePlan), ""},
		{[]string{"relay", "relay:failed"}, relayComment(t, 4, protocol.StageError, &protocol.ErrorMarker{Stage: protocol.StageTriage}), "/retry"},
		{[]string{"relay:discuss"}, relayComment(t, 5, protocol.StageError, &protocol.ErrorMarker{Stage: protocol.StageDiscuss}), ""},
	}
	for _, tc := range cases {
		labels, _ := protocol.ParseLabels(tc.labels)
		cm := protocol.Comment{ID: tc.cm.ID, Author: tc.cm.Author, Body: tc.cm.Body}
		out := Format("todo", repo, issue, cm, labels, noButtons)
		if len(out.Buttons) != 0 {
			t.Errorf("comment %d: buttons %+v", tc.cm.ID, out.Buttons)
		}
		line := "↩️ Відповідайте на це повідомлення: " + tc.want + "\nhttps://github.com/masteine/relay-sandbox/issues/5#issuecomment-"
		if tc.want == "" {
			if strings.Contains(out.Text, "↩️") {
				t.Errorf("comment %d: reply line without a command:\n%s", tc.cm.ID, out.Text)
			}
		} else if !strings.Contains(out.Text, "\n\n"+line) {
			t.Errorf("comment %d: want %q before the link:\n%s", tc.cm.ID, tc.want, out.Text)
		}
		tg := Format("todo", repo, issue, cm, labels, transport.TelegramCaps)
		if tc.want != "" && (len(tg.Buttons) != 1 || strings.Contains(tg.Text, "↩️")) {
			t.Errorf("comment %d: Telegram output changed: %+v", tc.cm.ID, tg)
		}
	}
	// The reply line counts in the budget, in runes for a rune-counting chat.
	body, _ := protocol.FormatStageComment(protocol.StageComment{Stage: protocol.StageDiscuss, Body: strings.Repeat("😀", 3000), Ack: protocol.Ack{Upto: 1}})
	small := transport.Caps{MaxText: 1000}
	out := Format("todo", repo, issue, protocol.Comment{ID: 9, Author: protocol.BotLogin, Body: body}, protocol.Labels{State: protocol.StateDiscuss}, small)
	if n := TextLen(out.Text, false); n > small.MaxText || n < small.MaxText-2 {
		t.Errorf("%d runes, max %d", n, small.MaxText)
	}
	if !strings.Contains(out.Text, "…\n\n↩️ Відповідайте на це повідомлення: /ticket\nhttps://") {
		t.Errorf("tail lost:\n%s", out.Text[len(out.Text)-200:])
	}
	if TextLen("😀a", true) != 3 || TextLen("😀a", false) != 2 || Truncate("😀😀😀", 2, false) != "😀…" || Truncate("😀😀😀", 3, true) != "😀…" {
		t.Error("units")
	}
}

func TestFormatPerChatInForwardAndHint(t *testing.T) {
	is := gh.Issue{Number: 2, Title: "T", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{relayComment(t, 101, protocol.StageDiscuss)}}
	h, wa, _ := whatsAppHarness(t, is)
	if err := h.core.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.tr.Count() != 1 || len(h.tr.Last().Msg.Buttons) != 1 || strings.Contains(h.tr.Last().Msg.Text, "↩️") {
		t.Errorf("telegram got %+v", h.tr.Sent)
	}
	if wa.Count() != 1 || wa.Last().Chat != waJID || len(wa.Last().Msg.Buttons) != 0 || !strings.Contains(wa.Last().Msg.Text, "/ticket") {
		t.Errorf("whatsapp got %+v", wa.Sent)
	}
	if reactions(t, h.f, 101) != 1 {
		t.Error("one reaction")
	}
	const swipe = "Проведіть пальцем праворуч по повідомленню Relay, щоб відповісти; команди — /ticket, /approve, /retry."
	_ = h.core.Handle(context.Background(), transport.Incoming{Chat: "wa:" + waJID, User: waJID, Private: true, Text: "hello"})
	if wa.Count() != 2 || !strings.Contains(wa.Last().Msg.Text, "Напишіть «<аліас>: задача»") || !strings.HasSuffix(wa.Last().Msg.Text, "\n"+swipe) {
		t.Errorf("whatsapp hint = %q", wa.Last().Msg.Text)
	}
	_ = h.core.Handle(context.Background(), msg("hello"))
	if h.tr.Count() != 2 || strings.Contains(h.tr.Last().Msg.Text, "Проведіть") {
		t.Errorf("telegram hint = %q", h.tr.Last().Msg.Text)
	}
}

func TestCommandReplyIsLowercased(t *testing.T) {
	h, wa, _ := whatsAppHarness(t, gh.Issue{Number: 2, Title: "T", Labels: []string{"relay", "relay:plan", "needs-approval"}})
	for i, tc := range []struct{ in, want string }{
		{"/Approve", "/approve"},
		{"/APPROVE", "/approve"},
		{"/Retry зараз", "/retry зараз"},
		{"/Usr/bin зламався", "/Usr/bin зламався"},
		{"Так\n/Approve", "Так\n/Approve"},
	} {
		in := transport.Incoming{Chat: "wa:" + waJID, User: waJID, Private: true, Text: tc.in, ReplyTo: "todo · masteine/relay-sandbox#2 · T\n\n…"}
		if err := h.core.Handle(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if got := h.f.LastComment(2); got != tc.want {
			t.Errorf("%q reached GitHub as %q, want %q", tc.in, got, tc.want)
		}
		if wa.Count() != i+1 || !strings.Contains(wa.Last().Msg.Text, "📨 masteine/relay-sandbox#2") {
			t.Errorf("confirmation = %+v", wa.Last())
		}
	}
}

// pollGate holds ListUpdated until the test opens it, so that Run is stuck
// in a Poll.
type pollGate struct {
	*gh.Fake
	entered chan struct{}
	open    chan struct{}
	once    sync.Once
}

func (g *pollGate) ListUpdated(ctx context.Context, since time.Time) ([]gh.Issue, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.open
	return g.Fake.ListUpdated(ctx, since)
}

// statusRun runs the core over a Mux of a Telegram Fake and a WhatsApp Fake
// that reports statuses.
type statusRun struct {
	h    *harness
	wa   *transport.Fake
	mux  *transport.Mux
	log  *syncBuffer
	stop func()
}

func startStatusRun(t *testing.T, gate *pollGate) *statusRun {
	t.Helper()
	h, wa, mux := whatsAppHarness(t)
	wa.Statuses = make(chan transport.Status)
	r := &statusRun{h: h, wa: wa, mux: mux, log: &syncBuffer{}}
	h.core.Log = r.log
	h.core.Config.Poll = time.Hour
	if gate != nil {
		gate.Fake = h.f
		h.core.Clients = func(gh.Repo) (gh.Client, error) { return gate, nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.core.Run(ctx) }()
	r.stop = func() {
		cancel()
		<-done
	}
	return r
}

// send delivers a status and waits until Run has logged it.
func (r *statusRun) send(t *testing.T, st transport.Status) {
	t.Helper()
	before := strings.Count(r.log.String(), "transport wa ")
	select {
	case r.wa.Statuses <- st:
	case <-time.After(5 * time.Second):
		t.Fatal("the adapter blocked on a status")
	}
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(r.log.String(), "transport wa ") == before {
		if time.Now().After(deadline) {
			t.Fatalf("status %+v not logged:\n%s", st, r.log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *statusRun) notices() []string {
	var out []string
	for _, s := range r.h.tr.Since(0) {
		if s.Chat != chat {
			continue
		}
		out = append(out, s.Msg.Text)
	}
	return out
}

func TestStatusUpDownUpNotifiesOtherMessengersOnce(t *testing.T) {
	r := startStatusRun(t, nil)
	defer r.stop()
	r.send(t, transport.Status{Up: true})
	if n := r.notices(); len(n) != 0 {
		t.Fatalf("first up: %q", n)
	}
	r.send(t, transport.Status{Up: false, Reason: "connect failure"})
	r.send(t, transport.Status{Up: false, Reason: "connect failure"})
	want := []string{"⚠️ WhatsApp відключено: connect failure. relay setup --whatsapp"}
	if n := r.notices(); strings.Join(n, "|") != strings.Join(want, "|") {
		t.Fatalf("after down, down: %q", n)
	}
	r.send(t, transport.Status{Up: true})
	r.send(t, transport.Status{Up: true})
	want = append(want, "✅ WhatsApp знову на зв'язку")
	if n := r.notices(); strings.Join(n, "|") != strings.Join(want, "|") {
		t.Fatalf("after up, up: %q", n)
	}
	if r.wa.Count() != 0 {
		t.Errorf("notices about WhatsApp went to WhatsApp: %+v", r.wa.Sent)
	}
	if !strings.Contains(r.log.String(), "relay bot: transport wa down: connect failure\n") || !strings.Contains(r.log.String(), "relay bot: transport wa up\n") {
		t.Errorf("log:\n%s", r.log.String())
	}
}

func TestStatusEachOutageNotifiesOnce(t *testing.T) {
	r := startStatusRun(t, nil)
	defer r.stop()
	r.send(t, transport.Status{Up: true})
	r.send(t, transport.Status{Up: false, Reason: "connect failure"})
	r.send(t, transport.Status{Up: true})
	r.send(t, transport.Status{Up: false, Reason: "logged out"})
	r.send(t, transport.Status{Up: false, Reason: "logged out"})
	want := []string{
		"⚠️ WhatsApp відключено: connect failure. relay setup --whatsapp",
		"✅ WhatsApp знову на зв'язку",
		"⚠️ WhatsApp відключено: logged out. relay setup --whatsapp",
	}
	if n := r.notices(); strings.Join(n, "|") != strings.Join(want, "|") {
		t.Errorf("notices: %q", n)
	}
}

func TestStatusDownWithoutReason(t *testing.T) {
	r := startStatusRun(t, nil)
	defer r.stop()
	r.send(t, transport.Status{Up: true})
	r.send(t, transport.Status{Up: false})
	if n := r.notices(); len(n) != 1 || n[0] != "⚠️ WhatsApp відключено. relay setup --whatsapp" {
		t.Errorf("notices: %q", n)
	}
}

func TestStatusUnknownMessengerIsOnlyLogged(t *testing.T) {
	h, wa, _ := whatsAppHarness(t)
	log := &syncBuffer{}
	h.core.Log = log
	h.core.linkStatus(context.Background(), &link{up: true, seen: true}, transport.Status{Messenger: "sig", Reason: "gone", Notify: true})
	if h.tr.Count() != 0 || wa.Count() != 0 || !strings.Contains(log.String(), "transport sig down: gone\n") || !strings.Contains(log.String(), "unknown messenger") {
		t.Errorf("tg=%d wa=%d log=%q", h.tr.Count(), wa.Count(), log.String())
	}
}

func TestStatusAtStartIsOnlyLoggedWithoutNotify(t *testing.T) {
	r := startStatusRun(t, nil)
	defer r.stop()
	r.send(t, transport.Status{Up: false, Reason: "not paired"})
	r.send(t, transport.Status{Up: false, Reason: "connect failure"})
	r.send(t, transport.Status{Up: true})
	if n := r.notices(); len(n) != 0 {
		t.Errorf("notices: %q", n)
	}
	if !strings.Contains(r.log.String(), "transport wa down: not paired") {
		t.Errorf("log:\n%s", r.log.String())
	}
}

func TestStatusLoggedOutAtStartNotifiesOnce(t *testing.T) {
	r := startStatusRun(t, nil)
	defer r.stop()
	r.send(t, transport.Status{Up: false, Reason: "logged out", Notify: true})
	r.send(t, transport.Status{Up: false, Reason: "logged out", Notify: true})
	r.send(t, transport.Status{Up: false, Reason: "logged out"})
	if n := r.notices(); len(n) != 1 || n[0] != "⚠️ WhatsApp відключено: logged out. relay setup --whatsapp" {
		t.Fatalf("notices: %q", n)
	}
	r.send(t, transport.Status{Up: true})
	if n := r.notices(); len(n) != 2 || n[1] != "✅ WhatsApp знову на зв'язку" {
		t.Errorf("notices: %q", n)
	}
}

func TestStatusDoesNotBlockAdapterWhileRunPolls(t *testing.T) {
	gate := &pollGate{entered: make(chan struct{}), open: make(chan struct{})}
	r := startStatusRun(t, gate)
	defer r.stop()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not start polling")
	}
	// Run is stuck in Poll: the adapter keeps going, the fail-fast flag
	// follows at once, and nothing is sent from outside Run.
	for i := 0; i < 50; i++ {
		select {
		case r.wa.Statuses <- transport.Status{Up: i%2 == 1, Reason: "flap"}:
		case <-time.After(2 * time.Second):
			t.Fatalf("adapter blocked on status %d while Run polls", i)
		}
	}
	r.wa.Statuses <- transport.Status{Up: false, Reason: "final"}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		_, err := r.mux.Send(context.Background(), transport.ChatID("wa:"+waJID), transport.Outgoing{Text: "x"})
		if errors.Is(err, transport.ErrDown) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want ErrDown while Run is busy, got %v", err)
		}
	}
	if n := r.notices(); len(n) != 0 {
		t.Fatalf("a notice was sent while Run was in Poll: %q", n)
	}
	close(gate.open)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.log.String(), "transport wa down: final") {
		if time.Now().After(deadline) {
			t.Fatalf("latest status lost:\n%s", r.log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if lines := strings.Count(r.log.String(), "transport wa "); lines > 3 {
		t.Errorf("%d statuses reached Run; older ones must be replaced", lines)
	}
}
