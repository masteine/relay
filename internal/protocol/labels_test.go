package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLabels(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    Labels
		wantErr string
	}{
		{
			name: "row 1: the bot creates the Issue",
			in:   []string{"relay:discuss"},
			want: Labels{State: StateDiscuss},
		},
		{
			name: "in pipeline with user labels",
			in:   []string{"bug", "relay", "relay:plan", "needs-approval", "android"},
			want: Labels{State: StatePlan, Pipeline: true, NeedsApproval: true, Other: []string{"android", "bug"}},
		},
		{
			name: "manual start: relay without state",
			in:   []string{"relay"},
			want: Labels{Pipeline: true},
		},
		{
			name: "no relay labels at all",
			in:   []string{"bug"},
			want: Labels{Other: []string{"bug"}},
		},
		{
			name: "unknown relay:* label belongs to the user",
			in:   []string{"relay:urgent", "relay:discuss"},
			want: Labels{State: StateDiscuss, Other: []string{"relay:urgent"}},
		},
		{
			name:    "two state labels",
			in:      []string{"relay:plan", "relay", "relay:triage"},
			wantErr: "more than one state label: relay:plan, relay:triage",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLabels(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLabelsStringsOrder(t *testing.T) {
	l := Labels{State: StatePlan, Pipeline: true, NeedsApproval: true, Other: []string{"android", "bug"}}
	got := strings.Join(l.Strings(), " ")
	want := "android bug relay relay:plan needs-approval"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestValidate(t *testing.T) {
	if err := (Labels{State: StateDiscuss}).Validate(); err != nil {
		t.Errorf("one state label: %v", err)
	}
	if err := (Labels{Pipeline: true}).Validate(); err == nil {
		t.Error("no state label: want error")
	}
}

func TestDiff(t *testing.T) {
	cur, _ := ParseLabels([]string{"bug", "relay", "relay:plan", "needs-approval"})
	next, _ := ParseLabels([]string{"bug", "relay", "relay:failed"})
	add, remove := Diff(cur, next)
	if !reflect.DeepEqual(add, []string{"relay:failed"}) {
		t.Errorf("add = %v", add)
	}
	if !reflect.DeepEqual(remove, []string{"relay:plan", "needs-approval"}) {
		t.Errorf("remove = %v", remove)
	}
	if a, r := Diff(cur, cur); a != nil || r != nil {
		t.Errorf("Diff(x, x) = %v, %v", a, r)
	}
}

// TestApply covers every row of the transition table in docs/protocol.md,
// plus the transitions that are not allowed.
func TestApply(t *testing.T) {
	type row struct {
		name  string
		cur   []string
		tr    Transition
		stage Stage
		want  []string
		deny  bool
	}
	rows := []row{
		// Allowed rows of the table.
		{name: "/ticket", cur: []string{"relay:discuss"}, tr: TransitionTicket, want: []string{"relay", "relay:triage"}},
		{name: "manual start", cur: []string{"relay"}, tr: TransitionManualStart, want: []string{"relay", "relay:triage"}},
		{name: "triage done", cur: []string{"relay", "relay:triage"}, tr: TransitionTriageDone, want: []string{"relay", "relay:plan"}},
		{name: "plan done", cur: []string{"relay", "relay:plan"}, tr: TransitionPlanDone, want: []string{"relay", "relay:plan", "needs-approval"}},
		{name: "/approve after plan (v0)", cur: []string{"relay", "relay:plan", "needs-approval"}, tr: TransitionApprove, want: []string{"relay", "relay:plan"}},
		{name: "triage failed", cur: []string{"relay", "relay:triage"}, tr: TransitionFailed, stage: StageTriage, want: []string{"relay", "relay:failed"}},
		{name: "plan failed drops needs-approval", cur: []string{"relay", "relay:plan", "needs-approval"}, tr: TransitionFailed, stage: StagePlan, want: []string{"relay", "relay:failed"}},
		{name: "ticket failed", cur: []string{"relay:discuss"}, tr: TransitionFailed, stage: StageTicket, want: []string{"relay", "relay:failed"}},
		{name: "discuss failed keeps labels", cur: []string{"relay:discuss"}, tr: TransitionFailed, stage: StageDiscuss, want: []string{"relay:discuss"}},
		{name: "approve failed keeps labels", cur: []string{"relay", "relay:plan", "needs-approval"}, tr: TransitionFailed, stage: StageApprove, want: []string{"relay", "relay:plan", "needs-approval"}},
		{name: "/retry triage", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StageTriage, want: []string{"relay", "relay:triage"}},
		{name: "/retry plan", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StagePlan, want: []string{"relay", "relay:plan"}},
		{name: "/retry ticket", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StageTicket, want: []string{"relay", "relay:discuss"}},
		{name: "user labels survive", cur: []string{"bug", "relay:urgent", "relay:discuss"}, tr: TransitionTicket, want: []string{"bug", "relay:urgent", "relay", "relay:triage"}},

		// Not allowed.
		{name: "/ticket twice", cur: []string{"relay", "relay:triage"}, tr: TransitionTicket, deny: true},
		{name: "/approve without needs-approval", cur: []string{"relay", "relay:plan"}, tr: TransitionApprove, deny: true},
		{name: "/approve outside plan", cur: []string{"relay:discuss", "needs-approval"}, tr: TransitionApprove, deny: true},
		{name: "plan done twice", cur: []string{"relay", "relay:plan", "needs-approval"}, tr: TransitionPlanDone, deny: true},
		{name: "triage done from plan", cur: []string{"relay", "relay:plan"}, tr: TransitionTriageDone, deny: true},
		{name: "manual start on an issue with state", cur: []string{"relay", "relay:plan"}, tr: TransitionManualStart, deny: true},
		{name: "manual start without relay label", cur: []string{}, tr: TransitionManualStart, deny: true},
		{name: "/retry when not failed", cur: []string{"relay", "relay:plan"}, tr: TransitionRetry, stage: StagePlan, deny: true},
		{name: "/retry approve", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StageApprove, deny: true},
		{name: "/retry discuss", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StageDiscuss, deny: true},
		{name: "/retry of a v1 stage", cur: []string{"relay", "relay:failed"}, tr: TransitionRetry, stage: StageImplement, deny: true},
		{name: "unknown transition", cur: []string{"relay:discuss"}, tr: Transition("bogus"), deny: true},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			cur, err := ParseLabels(r.cur)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Apply(cur, r.tr, r.stage)
			if r.deny {
				if err == nil {
					t.Fatalf("want error, got %v", got.Strings())
				}
				if !reflect.DeepEqual(got, cur) {
					t.Errorf("denied transition changed labels: %v", got.Strings())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Strings(), r.want) {
				t.Errorf("got %v, want %v", got.Strings(), r.want)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("result breaks the invariant: %v", err)
			}
		})
	}
}

// TestApplyKeepsInvariant tries every transition and stage from every valid
// label set and checks that whatever is allowed keeps exactly one state label.
func TestApplyKeepsInvariant(t *testing.T) {
	transitions := []Transition{TransitionTicket, TransitionManualStart, TransitionTriageDone, TransitionPlanDone, TransitionApprove, TransitionFailed, TransitionRetry}
	stages := []Stage{StageDiscuss, StageTicket, StageTriage, StagePlan, StageApprove, StageImplement, StageIgnored, StageError, ""}
	var starts []Labels
	for _, s := range AllStates {
		for _, pipeline := range []bool{false, true} {
			for _, na := range []bool{false, true} {
				starts = append(starts, Labels{State: s, Pipeline: pipeline, NeedsApproval: na, Other: []string{"bug"}})
			}
		}
	}
	starts = append(starts, Labels{Pipeline: true}) // manual start
	allowed := 0
	for _, cur := range starts {
		for _, tr := range transitions {
			for _, st := range stages {
				next, err := Apply(cur, tr, st)
				if err != nil {
					if !reflect.DeepEqual(next, cur) {
						t.Errorf("%v %s %s: denied but changed", cur.Strings(), tr, st)
					}
					continue
				}
				allowed++
				// Before manual start the Issue has no state yet. From there
				// an allowed transition is either a no-op (a failed discuss
				// or approve) or produces a valid set (manual start, or a
				// stage that failed right after the manual start).
				if cur.State == "" && reflect.DeepEqual(next, cur) {
					continue
				}
				if err := next.Validate(); err != nil {
					t.Errorf("%v %s %s → %v: %v", cur.Strings(), tr, st, next.Strings(), err)
				}
				if !reflect.DeepEqual(next.Other, cur.Other) {
					t.Errorf("%v %s %s: user labels changed", cur.Strings(), tr, st)
				}
			}
		}
	}
	if allowed == 0 {
		t.Fatal("no transition was allowed; the test is broken")
	}
}
