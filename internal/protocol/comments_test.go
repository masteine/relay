package protocol

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden file\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// goldenComments are the comments every stage posts in v0. Each one is
// rendered, compared with testdata/golden/<name>.md and parsed back.
var goldenComments = map[string]StageComment{
	"comment-discuss.md": {
		Stage:   StageDiscuss,
		Body:    "Зрозумів: темна тема має перемикатися з налаштувань і стежити за системною.\n\nДва уточнення:\n1. Чи потрібен окремий перемикач «як у системі»?\n2. Чи міняємо іконку застосунку?",
		Ack:     Ack{Upto: 101},
		Usage:   &Usage{Turns: 6, In: 18000, Out: 2000, CacheRead: 46000, CacheCreate: 48000, Ms: 240000, USD: 0.09, Model: "sonnet"},
		Session: &Session{ID: "3f21d401-cced-406f-957d-75f050269450"},
	},
	"comment-ticket.md": {
		Stage: StageTicket,
		Body:  "Задачу записано в тіло Issue. Далі: тріаж і план.",
		Ack:   Ack{Upto: 104},
		Usage: &Usage{Turns: 2, In: 9000, Out: 600, Ms: 41000, USD: 0.03, Model: "sonnet"},
	},
	"comment-triage.md": {
		Stage: StageTriage,
		Body:  "**Обсяг:** M\n**Дизайн:** не потрібен\n**Ключові файли:**\n- `app/src/main/java/com/example/theme/Theme.kt`\n- `app/src/main/res/values/themes.xml`",
		Ack:   Ack{Upto: 104},
		Usage: &Usage{Turns: 12, In: 95000, Out: 4000, CacheRead: 300000, CacheCreate: 55000, Ms: 180000, USD: 0.35, Model: "opus"},
	},
	"comment-plan.md": {
		Stage: StagePlan,
		Body:  "**ADR.** Перемикач у налаштуваннях, стан у DataStore, системна тема за замовчуванням.\n\nПлан: [`docs/specs/42-dodaty-temnu-temu.md`](../blob/relay/42-dodaty-temnu-temu/docs/specs/42-dodaty-temnu-temu.md)\n\nВердикт critic: APPROVE після 2 ітерацій.\n\nНатисніть ✅ у Telegram або напишіть `/approve`.",
		Ack:   Ack{Upto: 104},
		Usage: &Usage{Turns: 40, In: 410000, Out: 22000, CacheRead: 1200000, CacheCreate: 60000, Ms: 660000, USD: 1.60, Model: "opus"},
	},
	"comment-approve.md": {
		Stage: StageApprove,
		Body:  "План підтверджено. У v0 пайплайн зупиняється тут.",
		Ack:   Ack{Upto: 110},
	},
	"comment-error.md": {
		Stage: StageError,
		Body:  "Етап **plan** перевищив дедлайн 42 хв.\n\nНапишіть `/retry`, щоб повторити з цього етапу, або натисніть 🔁 у Telegram.",
		Ack:   Ack{Upto: 104, Run: "39600754181"},
		// The usage of an error comment belongs to the stage that failed.
		Usage: &Usage{Stage: StagePlan, Turns: 200, In: 2100000, Out: 90000, Ms: 2520000, USD: 7.80, Model: "opus"},
		Error: &ErrorMarker{Stage: StagePlan, Reason: "deadline", Run: "39600754181"},
	},
	"comment-ignored.md": {
		Stage: StageIgnored,
		Body:  "`/approve` проігноровано: /approve needs relay:plan with needs-approval",
		Ack:   Ack{Upto: 112},
	},
}

func TestFormatStageCommentGolden(t *testing.T) {
	for name, c := range goldenComments {
		t.Run(name, func(t *testing.T) {
			got, err := FormatStageComment(c)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name, got)

			if !IsRelayComment(got) {
				t.Error("formatted comment is not recognised as a Relay comment")
			}
			back, err := ParseStageComment(got)
			if err != nil {
				t.Fatal(err)
			}
			want := c
			want.Body = strings.TrimSpace(c.Body)
			want.Ack.V = Version
			if want.Usage != nil {
				u := *want.Usage
				u.V = Version
				if u.Stage == "" {
					u.Stage = c.Stage
				}
				want.Usage = &u
			}
			if want.Session != nil {
				s := *want.Session
				s.V = Version
				want.Session = &s
			}
			if want.Error != nil {
				e := *want.Error
				e.V = Version
				want.Error = &e
			}
			if !reflect.DeepEqual(back, want) {
				t.Errorf("round trip\n got %+v\nwant %+v", back, want)
			}
		})
	}
}

func TestFormatStageCommentRejectsEmptyStage(t *testing.T) {
	if _, err := FormatStageComment(StageComment{}); err == nil {
		t.Error("want error for empty stage")
	}
}

// TestBodyCannotInjectMarkers: a body that quotes a marker (likely when Relay
// works on its own repository) must not override the comment's own markers.
func TestBodyCannotInjectMarkers(t *testing.T) {
	body := "Quoting the protocol:\n\n<!-- relay-ack {\"v\":1,\"upto\":999} -->\n<!-- relay-error {\"v\":1,\"stage\":\"ticket\"} -->\n\nend of quote"
	got, err := FormatStageComment(StageComment{Stage: StageDiscuss, Body: body, Ack: Ack{Upto: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "<!--") != 1 {
		t.Errorf("body markers were not escaped:\n%s", got)
	}
	back, err := ParseStageComment(got)
	if err != nil {
		t.Fatal(err)
	}
	if back.Ack.Upto != 7 {
		t.Errorf("Ack.Upto = %d, want 7", back.Ack.Upto)
	}
	if back.Error != nil {
		t.Errorf("Error = %+v, want nil", back.Error)
	}
	if !strings.Contains(back.Body, "&lt;!-- relay-ack") || !strings.HasSuffix(back.Body, "end of quote") {
		t.Errorf("Body = %q", back.Body)
	}

	// Even unescaped (an old comment, or a human's), a marker in the text
	// above the trailing block does not count.
	raw := "## Relay · discuss\n\n<!-- relay-ack {\"v\":1,\"upto\":999} -->\ntext after it\n\n<!-- relay-ack {\"v\":1,\"upto\":7} -->\n"
	if a, ok, err := ParseAck(raw); err != nil || !ok || a.Upto != 7 {
		t.Errorf("ParseAck(raw) = %+v,%v,%v", a, ok, err)
	}
}

func TestParseHeading(t *testing.T) {
	tests := []struct {
		in   string
		want Stage
		ok   bool
	}{
		{"## Relay · plan\n\nbody", StagePlan, true},
		{"\n\n## Relay · triage  \nbody", StageTriage, true},
		{"## Relay · plan", StagePlan, true},
		{"## Relay · summary\n", Stage("summary"), true}, // a v1 heading still counts as Relay's
		{"## Relay · \n", "", false},
		{"## Relay · two words\n", "", false},
		{"## Relay - plan\n", "", false},
		{"Relay · plan\n", "", false},
		{"text\n## Relay · plan\n", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		got, ok := ParseHeading(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseHeading(%q) = %q,%v want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMarkers(t *testing.T) {
	body := "## Relay · plan\n\ntext with <!-- an unrelated comment --> inside\n\n" +
		"<!-- relay-ack {\"v\":1,\"upto\":77} -->\n" +
		"<!-- relay-usage {\"v\":1,\"stage\":\"plan\",\"turns\":3,\"in\":10,\"out\":20,\"cache_read\":0,\"cache_create\":0,\"ms\":1500,\"usd\":0.01,\"model\":\"opus\"} -->\n" +
		"<!-- relay-error {\"v\":1,\"stage\":\"plan\",\"reason\":\"x \\u003e y\"} -->\n"

	a, ok, err := ParseAck(body)
	if err != nil || !ok || a.Upto != 77 {
		t.Errorf("ParseAck = %+v,%v,%v", a, ok, err)
	}
	u, ok, err := ParseUsage(body)
	if err != nil || !ok || u.Turns != 3 || u.Model != "opus" {
		t.Errorf("ParseUsage = %+v,%v,%v", u, ok, err)
	}
	e, ok, err := ParseError(body)
	if err != nil || !ok || e.Reason != "x > y" {
		t.Errorf("ParseError = %+v,%v,%v", e, ok, err)
	}
	if _, ok, err := ParseSession(body); ok || err != nil {
		t.Errorf("ParseSession on a body without one = %v,%v", ok, err)
	}

	if _, _, err := ParseAck("<!-- relay-ack {not json} -->"); err == nil {
		t.Error("malformed marker: want error")
	}
	if a, ok, _ := ParseAck("<!-- relay-ack {\"v\":1,\"upto\":1} --> <!-- relay-ack {\"v\":1,\"upto\":5} -->"); !ok || a.Upto != 5 {
		t.Errorf("last marker must win, got %+v,%v", a, ok)
	}
	if _, _, err := ParseAck("<!-- relay-ack {\"v\":2,\"upto\":5} -->"); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("marker from a newer protocol: err = %v", err)
	}
}

func TestMarkerCannotCloseEarly(t *testing.T) {
	// A reason containing "-->" must not terminate the HTML comment.
	s, err := formatMarker(markerError, ErrorMarker{V: 1, Stage: StagePlan, Reason: "a --> b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(s, "-->") != 1 {
		t.Errorf("marker closes early: %s", s)
	}
	e, ok, err := ParseError(s)
	if err != nil || !ok || e.Reason != "a --> b" {
		t.Errorf("round trip = %+v,%v,%v", e, ok, err)
	}
}

func TestUsageFooter(t *testing.T) {
	tests := []struct {
		u    Usage
		want string
	}{
		{Usage{Turns: 40, In: 410000, Out: 22000, Ms: 660000, USD: 1.6, Model: "opus"}, "Ходи: 40 · Токени: 410k ↓ / 22k ↑ · Час: 11 хв · ≈$1.60 · Модель: opus"},
		{Usage{Turns: 85, In: 1200000, Out: 60000, Ms: 1440000, USD: 4.5}, "Ходи: 85 · Токени: 1.2M ↓ / 60k ↑ · Час: 24 хв · ≈$4.50"},
		{Usage{Turns: 1, In: 4, Out: 278, Ms: 6100, USD: 0.0235}, "Ходи: 1 · Токени: 4 ↓ / 278 ↑ · Час: 6 с · ≈$0.02"},
		{Usage{Turns: 2, In: 1500, Out: 999, Ms: 3_900_000, USD: 0}, "Ходи: 2 · Токени: 2k ↓ / 999 ↑ · Час: 1 год 5 хв · ≈$0.00"},
	}
	for _, tc := range tests {
		if got := UsageFooter(tc.u); got != tc.want {
			t.Errorf("UsageFooter(%+v)\n got %q\nwant %q", tc.u, got, tc.want)
		}
	}
}

func TestParseStageCommentWithoutMarkers(t *testing.T) {
	c, err := ParseStageComment("## Relay · triage\n\nold comment from before acks\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Stage != StageTriage || c.Body != "old comment from before acks" || c.Ack.Upto != 0 {
		t.Errorf("got %+v", c)
	}
	if _, err := ParseStageComment("just text"); err == nil {
		t.Error("want error for a non-Relay comment")
	}
}

// FuzzRoundTrip: whatever the body, the comment's own markers survive and the
// body comes back as rendered.
func FuzzRoundTrip(f *testing.F) {
	for _, s := range []string{
		"", "plain", "---\n", "\n---\nfake footer", "<!-- relay-ack {\"v\":1,\"upto\":999} -->",
		"line\n<!-- relay-error {\"v\":1,\"stage\":\"x\"} -->\n", "## Relay · fake\n", "a --> b", "`code`\n\n- list",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		in := StageComment{
			Stage: StagePlan,
			Body:  body,
			Ack:   Ack{Upto: 7},
			Usage: &Usage{Stage: StagePlan, Turns: 1, Ms: 1000, Model: "opus"},
			Error: &ErrorMarker{Stage: StageTriage, Reason: "r"},
		}
		s, err := FormatStageComment(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := ParseStageComment(s)
		if err != nil {
			t.Fatalf("parse: %v\n%s", err, s)
		}
		if out.Ack.Upto != 7 || out.Usage == nil || out.Usage.Turns != 1 || out.Error == nil || out.Error.Stage != StageTriage {
			t.Fatalf("markers lost: %+v\n%s", out, s)
		}
		wantBody := strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(body), "<!--", "&lt;!--"))
		if out.Body != wantBody {
			t.Fatalf("body: got %q want %q\n%s", out.Body, wantBody, s)
		}
	})
}
