package core

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "golden", name)
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

// render prints a message as text followed by its buttons, one per line,
// so that a golden file shows both.
func render(out transport.Outgoing) string {
	var b strings.Builder
	b.WriteString(out.Text)
	b.WriteString("\n--- buttons ---\n")
	for _, btn := range out.Buttons {
		fmt.Fprintf(&b, "[%s] %s\n", btn.Label, btn.Data)
	}
	return b.String()
}

// TestFormatGolden pins what Format sends to Telegram for the stages that
// carry buttons, so that a change to the formatter shows up byte for byte.
func TestFormatGolden(t *testing.T) {
	repo := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	issue := gh.Issue{Number: 7, Title: "Темна тема", URL: "https://github.com/masteine/relay-sandbox/issues/7"}
	stage := func(c protocol.StageComment) string {
		body, err := protocol.FormatStageComment(c)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	discuss := stage(protocol.StageComment{Stage: protocol.StageDiscuss, Body: "Зрозумів: темна тема має перемикатися з налаштувань.\n\nДва уточнення:\n1. Окремий перемикач «як у системі»?\n2. Міняємо іконку? 🎨", Ack: protocol.Ack{Upto: 101}})
	plan := stage(protocol.StageComment{Stage: protocol.StagePlan, Body: "План у docs/specs/7-dark-theme.md.\n\n- Додати тему\n- Перемикач у налаштуваннях", Ack: protocol.Ack{Upto: 103}})
	fail := stage(protocol.StageComment{Stage: protocol.StageError, Body: "Етап triage впав: раннер недоступний.", Ack: protocol.Ack{Upto: 104}, Error: &protocol.ErrorMarker{Stage: protocol.StageTriage}})
	cases := []struct {
		name   string
		body   string
		id     int64
		labels []string
	}{
		{"format-discuss.txt", discuss, 102, []string{"relay:discuss"}},
		{"format-plan.txt", plan, 105, []string{"relay", "relay:plan"}},
		{"format-plan-approve.txt", plan, 105, []string{"relay", "relay:plan", "needs-approval"}},
		{"format-error.txt", fail, 106, []string{"relay", "relay:failed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels, _ := protocol.ParseLabels(tc.labels)
			out := Format("todo", repo, issue, protocol.Comment{ID: tc.id, Author: protocol.BotLogin, Body: tc.body}, labels)
			golden(t, tc.name, render(out))
		})
	}
}
