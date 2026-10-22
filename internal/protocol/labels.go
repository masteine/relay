package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// Version is the protocol version. Every marker carries it and `relay stage`
// refuses to run when the workflow's RELAY_PROTOCOL differs.
const Version = 1

const (
	// LabelPipeline marks an Issue that has passed /ticket and is in the
	// pipeline. It stays until the Issue is done or failed.
	LabelPipeline = "relay"
	// LabelNeedsApproval marks an Issue that waits for a human decision.
	LabelNeedsApproval = "needs-approval"
)

// State is the relay:* label that says where an Issue is. An Issue carries at
// most one of them; after creation, exactly one.
type State string

const (
	StateDiscuss   State = "relay:discuss"
	StateTriage    State = "relay:triage"
	StatePlan      State = "relay:plan"
	StateImplement State = "relay:implement"
	StateTest      State = "relay:test"
	StateReview    State = "relay:review"
	StateDeploy    State = "relay:deploy"
	StateDone      State = "relay:done"
	StateFailed    State = "relay:failed"
	StateQueued    State = "relay:queued"
)

// AllStates lists every state label, in pipeline order.
var AllStates = []State{
	StateDiscuss, StateTriage, StatePlan, StateImplement, StateTest,
	StateReview, StateDeploy, StateDone, StateFailed, StateQueued,
}

// IsState reports whether label is one of the known state labels.
func IsState(label string) bool {
	for _, s := range AllStates {
		if string(s) == label {
			return true
		}
	}
	return false
}

// Labels is the parsed label set of an Issue.
type Labels struct {
	// State is "" when the Issue has no state label (for example an Issue
	// a human created and then labelled `relay` to start it by hand).
	State         State
	Pipeline      bool
	NeedsApproval bool
	// Other holds labels Relay does not own, including relay:* names it does
	// not know. They are carried through unchanged so that a transition
	// never drops a user's label.
	Other []string
}

// ParseLabels reads an Issue's label names. It fails only on the one thing a
// transition cannot recover from: more than one state label.
func ParseLabels(names []string) (Labels, error) {
	var l Labels
	var states []string
	for _, n := range names {
		switch {
		case n == LabelPipeline:
			l.Pipeline = true
		case n == LabelNeedsApproval:
			l.NeedsApproval = true
		case IsState(n):
			states = append(states, n)
		default:
			l.Other = append(l.Other, n)
		}
	}
	sort.Strings(l.Other)
	switch len(states) {
	case 0:
	case 1:
		l.State = State(states[0])
	default:
		sort.Strings(states)
		return l, fmt.Errorf("more than one state label: %s", strings.Join(states, ", "))
	}
	return l, nil
}

// Validate checks the invariant that holds after an Issue was created by
// Relay: exactly one state label.
func (l Labels) Validate() error {
	if l.State == "" {
		return fmt.Errorf("no state label")
	}
	return nil
}

// Strings renders the label set in a fixed order: the user's labels sorted,
// then relay, then the state, then needs-approval.
func (l Labels) Strings() []string {
	out := make([]string, 0, len(l.Other)+3)
	out = append(out, l.Other...)
	if l.Pipeline {
		out = append(out, LabelPipeline)
	}
	if l.State != "" {
		out = append(out, string(l.State))
	}
	if l.NeedsApproval {
		out = append(out, LabelNeedsApproval)
	}
	return out
}

// Diff returns the labels to add and to remove to get from cur to next.
// The wrapper applies these two lists instead of replacing the whole label
// set, so that a label the owner adds while a stage runs is not lost.
func Diff(cur, next Labels) (add, remove []string) {
	have := map[string]bool{}
	for _, n := range cur.Strings() {
		have[n] = true
	}
	want := map[string]bool{}
	for _, n := range next.Strings() {
		want[n] = true
		if !have[n] {
			add = append(add, n)
		}
	}
	for _, n := range cur.Strings() {
		if !want[n] {
			remove = append(remove, n)
		}
	}
	return add, remove
}

// Transition is something that happened to an Issue and changes its labels.
type Transition string

const (
	// TransitionTicket: /ticket was handled, the task is in the pipeline.
	TransitionTicket Transition = "ticket"
	// TransitionManualStart: the owner added the `relay` label by hand to an
	// Issue without a state label.
	TransitionManualStart Transition = "manual-start"
	// TransitionTriageDone: the triage stage posted its comment.
	TransitionTriageDone Transition = "triage-done"
	// TransitionPlanDone: the plan stage posted its comment; a human decides.
	TransitionPlanDone Transition = "plan-done"
	// TransitionApprove: /approve was handled.
	TransitionApprove Transition = "approve"
	// TransitionFailed: a stage hit an error, its deadline or --max-turns.
	// The stage is passed to Apply. A failed discuss or approve keeps its
	// labels: the owner simply writes again.
	TransitionFailed Transition = "failed"
	// TransitionRetry: /retry was handled; the stage to resume is passed
	// to Apply. Only ticket, triage and plan can be resumed.
	TransitionRetry Transition = "retry"
)

// Apply returns the labels after transition tr. stage is used only by
// TransitionFailed (the stage that failed) and TransitionRetry (the stage to
// resume). An error means the transition is not allowed from cur; the caller
// treats that as "ignored" and changes nothing.
func Apply(cur Labels, tr Transition, stage Stage) (Labels, error) {
	next := cur
	next.Other = append([]string(nil), cur.Other...)
	switch tr {
	case TransitionTicket:
		if cur.State != StateDiscuss {
			return cur, notAllowed(tr, cur)
		}
		next.Pipeline = true
		next.State = StateTriage
	case TransitionManualStart:
		if cur.State != "" || !cur.Pipeline {
			return cur, notAllowed(tr, cur)
		}
		next.State = StateTriage
	case TransitionTriageDone:
		if cur.State != StateTriage {
			return cur, notAllowed(tr, cur)
		}
		next.Pipeline = true
		next.State = StatePlan
	case TransitionPlanDone:
		if cur.State != StatePlan || cur.NeedsApproval {
			return cur, notAllowed(tr, cur)
		}
		next.Pipeline = true
		next.NeedsApproval = true
	case TransitionApprove:
		if cur.State != StatePlan || !cur.NeedsApproval {
			return cur, notAllowed(tr, cur)
		}
		// v0 ends at the plan gate, so the state stays. v1 moves it to
		// relay:implement here.
		next.NeedsApproval = false
	case TransitionFailed:
		if stage == StageDiscuss || stage == StageApprove {
			return next, nil
		}
		next.Pipeline = true
		next.State = StateFailed
		next.NeedsApproval = false
	case TransitionRetry:
		if cur.State != StateFailed {
			return cur, notAllowed(tr, cur)
		}
		if StagesFrom(stage) == nil || stage == StageDiscuss {
			return cur, fmt.Errorf("retry: stage %q cannot be resumed", stage)
		}
		next.Pipeline = true
		next.State = EntryState(stage)
	default:
		return cur, fmt.Errorf("unknown transition %q", tr)
	}
	return next, nil
}

func notAllowed(tr Transition, cur Labels) error {
	return fmt.Errorf("transition %q not allowed from [%s]", tr, strings.Join(cur.Strings(), " "))
}
