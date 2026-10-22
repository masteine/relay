package stage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseEventIssueComment(t *testing.T) {
	ev, err := ParseEvent("issue_comment", fixture(t, "issue_comment.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := Event{
		Name: protocol.EventIssueComment, Action: "created", Sender: "masteine", RepoPrivate: true,
		Repo:        gh.Repo{Owner: "masteine", Name: "relay-sandbox"},
		IssueNumber: 42, Title: "Додати команду /status у бота", Body: "## Задача\nДодати в бота команду `/status`.",
		Labels:  []string{"relay", "relay:plan", "needs-approval"},
		Comment: &protocol.Comment{ID: 2100000111, Author: "masteine", Body: "/approve"},
	}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("\n got %+v\nwant %+v", ev, want)
	}
	if ev.Repo.String() != "masteine/relay-sandbox" {
		t.Errorf("Repo = %s", ev.Repo)
	}
}

func TestParseEventPullRequestComment(t *testing.T) {
	ev, err := ParseEvent("issue_comment", fixture(t, "issue_comment_pr.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsPullRequest || ev.IssueNumber != 7 {
		t.Errorf("got %+v", ev)
	}
}

func TestParseEventLabeled(t *testing.T) {
	ev, err := ParseEvent("issues", fixture(t, "issues_labeled.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Name != protocol.EventIssues || ev.Action != "labeled" || ev.Label != "relay" || ev.IssueNumber != 8 || ev.Comment != nil {
		t.Errorf("got %+v", ev)
	}
	if !reflect.DeepEqual(ev.Labels, []string{"bug", "relay"}) {
		t.Errorf("Labels = %v", ev.Labels)
	}
}

func TestParseEventDispatch(t *testing.T) {
	ev, err := ParseEvent("workflow_dispatch", fixture(t, "workflow_dispatch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Sender != protocol.BotLogin || ev.Dispatch == nil || ev.Dispatch.Issue != 11 || ev.Dispatch.Stage != protocol.StagePlan || ev.IssueNumber != 11 {
		t.Errorf("got %+v dispatch=%+v", ev, ev.Dispatch)
	}
	// Numbers arrive as JSON numbers in some contexts.
	ev, err = ParseEvent("workflow_dispatch", []byte(`{"inputs":{"issue":12,"stage":"discuss"},"sender":{"login":"masteine"},"repository":{"full_name":"a/b","private":true}}`))
	if err != nil || ev.Dispatch.Issue != 12 {
		t.Errorf("numeric input: %v %+v", err, ev.Dispatch)
	}
}

func TestParseEventErrors(t *testing.T) {
	tests := []struct{ name, event, body string }{
		{"not json", "issues", "{"},
		{"no repository", "issues", `{"action":"labeled","issue":{"number":1},"sender":{"login":"x"},"repository":{"full_name":"nope"}}`},
		{"comment without issue", "issue_comment", `{"action":"created","comment":{"id":1},"sender":{"login":"x"},"repository":{"full_name":"a/b"}}`},
		{"labeled without issue", "issues", `{"action":"labeled","sender":{"login":"x"},"repository":{"full_name":"a/b"}}`},
		{"dispatch bad issue", "workflow_dispatch", `{"inputs":{"issue":"abc"},"sender":{"login":"x"},"repository":{"full_name":"a/b"}}`},
	}
	for _, tc := range tests {
		if _, err := ParseEvent(tc.event, []byte(tc.body)); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
}

func TestWebhook(t *testing.T) {
	ev, _ := ParseEvent("issue_comment", fixture(t, "issue_comment.json"))
	issue := protocol.Issue{Number: 42, Labels: ev.Labels, Comments: []protocol.Comment{*ev.Comment}}
	w := ev.Webhook(issue)
	if w.Name != protocol.EventIssueComment || w.Sender != "masteine" || !w.RepoPrivate || w.Comment != ev.Comment || w.Issue.Number != 42 {
		t.Errorf("got %+v", w)
	}
	d := protocol.Route(w, "masteine")
	if len(d.Stages) != 1 || d.Stages[0] != protocol.StageApprove {
		t.Errorf("Route = %+v", d)
	}
}
