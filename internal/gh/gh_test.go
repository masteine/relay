package gh

import (
	"context"
	"reflect"
	"testing"

	"github.com/masteine/relay/internal/protocol"
)

func TestParseRepo(t *testing.T) {
	if r, err := ParseRepo("masteine/relay-sandbox"); err != nil || r != (Repo{"masteine", "relay-sandbox"}) || r.String() != "masteine/relay-sandbox" {
		t.Errorf("got %+v, %v", r, err)
	}
	for _, bad := range []string{"", "nope", "/x", "x/", "a/b/c", "a/..", "../b", "a/.", "a/b c", "-a/b"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestIssueProtocolSortsComments(t *testing.T) {
	is := Issue{Number: 3, Labels: []string{"relay:discuss"}, Comments: []Comment{{ID: 20, Author: "a", Body: "second"}, {ID: 10, Author: "a", Body: "first"}}}
	p := is.Protocol()
	if p.Number != 3 || len(p.Comments) != 2 || p.Comments[0].ID != 10 || p.Comments[1].ID != 20 {
		t.Errorf("got %+v", p)
	}
	p.Labels[0] = "changed"
	if is.Labels[0] != "relay:discuss" {
		t.Error("Protocol() must copy labels")
	}
}

func TestApplyLabelsAddsBeforeRemoving(t *testing.T) {
	f := NewFake(Issue{Number: 1, Labels: []string{"bug", "relay", "relay:plan", "needs-approval"}})
	cur, _ := protocol.ParseLabels([]string{"bug", "relay", "relay:plan", "needs-approval"})
	next, _ := protocol.Apply(cur, protocol.TransitionFailed, protocol.StagePlan)
	if err := ApplyLabels(context.Background(), f, 1, cur, next); err != nil {
		t.Fatal(err)
	}
	want := []string{"add #1 relay:failed", "remove #1 relay:plan", "remove #1 needs-approval"}
	if !reflect.DeepEqual(f.Log, want) {
		t.Errorf("Log = %v, want %v", f.Log, want)
	}
	got, _ := f.Issue(context.Background(), 1)
	if !reflect.DeepEqual(got.Labels, []string{"bug", "relay", "relay:failed"}) {
		t.Errorf("labels after = %v", got.Labels)
	}
	// Nothing to do → no calls.
	f.Log = nil
	if err := ApplyLabels(context.Background(), f, 1, next, next); err != nil || len(f.Log) != 0 {
		t.Errorf("no-op: err=%v log=%v", err, f.Log)
	}
}

func TestFakeComments(t *testing.T) {
	f := NewFake(Issue{Number: 1, Comments: []Comment{{ID: 5000, Author: "x", Body: "hi"}}})
	id, err := f.CreateComment(context.Background(), 1, "## Relay · discuss\n\nok")
	if err != nil || id != 5001 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if f.LastComment(1) != "## Relay · discuss\n\nok" {
		t.Errorf("LastComment = %q", f.LastComment(1))
	}
	if _, err := f.CreateComment(context.Background(), 2, "x"); err == nil {
		t.Error("unknown issue: want error")
	}
	if err := f.EditIssue(context.Background(), 1, "", "new body"); err != nil {
		t.Fatal(err)
	}
	is, _ := f.Issue(context.Background(), 1)
	if is.Body != "new body" || is.Title != "" {
		t.Errorf("after edit: %+v", is)
	}
}
