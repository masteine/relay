package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Comment is an Issue comment as reconcile sees it.
type Comment struct {
	ID     int64
	Author string
	Body   string
}

// Issue is the state reconcile works from: labels and all comments.
type Issue struct {
	Number   int
	Labels   []string
	Comments []Comment
}

// Decision is what route and reconcile return: which stages to run and why.
type Decision struct {
	Issue int
	// Stages is the `stages` output of the route job. Empty means nothing to
	// do. It contains "ignored" when the route job must post
	// "## Relay · ignored" with Reason; "discuss" may follow it.
	Stages []Stage
	Reason string
	// Upto is what the "ignored" comment acknowledges (see IgnoredAck): the
	// highest pending owner comment id, or the old watermark when discuss
	// runs as well. Stage jobs compute their own Ack with AckFor.
	Upto int64
	// Command is the command that led to the decision, if any.
	Command *ParsedCommand
}

// Ignored reports whether the decision includes posting an "ignored" comment.
func (d Decision) Ignored() bool {
	for _, s := range d.Stages {
		if s == StageIgnored {
			return true
		}
	}
	return false
}

// IgnoredAck is the Ack the route job puts on the "## Relay · ignored"
// comment. It is the decision's Upto, not AckFor(StageIgnored): when discuss
// is scheduled as well, Upto is the old watermark so that the reply stays
// pending for discuss.
func (d Decision) IgnoredAck(run string) Ack {
	return Ack{V: Version, Upto: d.Upto, Run: run}
}

// StagesJSON renders Stages for the workflow: always a JSON array, "[]" when
// there is nothing to do.
func (d Decision) StagesJSON() string {
	if len(d.Stages) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(d.Stages)
	return string(b)
}

func nothing(issue int, reason string) Decision {
	return Decision{Issue: issue, Reason: reason}
}

// sameLogin compares GitHub logins, which are case-insensitive.
func sameLogin(a, b string) bool {
	return a != "" && strings.EqualFold(a, b)
}

func sortedComments(issue Issue) []Comment {
	comments := append([]Comment(nil), issue.Comments...)
	sort.Slice(comments, func(i, j int) bool { return comments[i].ID < comments[j].ID })
	return comments
}

// Watermark returns the highest Ack.Upto among the Issue's stage comments, or
// 0 when it has none. Only comments by BotLogin count, so a human who pastes
// a Relay-looking comment cannot move the watermark.
func Watermark(issue Issue) int64 {
	var upto int64
	for _, c := range issue.Comments {
		if c.Author != BotLogin || !IsRelayComment(c.Body) {
			continue
		}
		if a, ok, err := ParseAck(c.Body); err == nil && ok && a.Upto > upto {
			upto = a.Upto
		}
	}
	return upto
}

// Pending returns the owner's comments above the watermark, in id order:
// the input the next stage has to answer. Comments by anyone else are not
// input.
func Pending(issue Issue, owner string) []Comment {
	upto := Watermark(issue)
	var out []Comment
	for _, c := range sortedComments(issue) {
		if c.ID > upto && sameLogin(c.Author, owner) {
			out = append(out, c)
		}
	}
	return out
}

// AckFor is the Ack a stage comment carries. Stages that read the owner's
// input (see ConsumesInput) acknowledge everything pending at that moment;
// the others repeat the current watermark, so that a comment written while a
// chain runs stays pending. run is the run identity, or "".
func AckFor(stage Stage, issue Issue, owner, run string) Ack {
	if ConsumesInput(stage) {
		return AckAll(issue, owner, run)
	}
	return Ack{V: Version, Upto: Watermark(issue), Run: run}
}

// AckAll acknowledges everything the owner has written so far. A stage that
// was started by /retry uses it whatever its kind, so that the /retry comment
// (and anything after it) does not stay pending.
func AckAll(issue Issue, owner, run string) Ack {
	upto := Watermark(issue)
	for _, c := range Pending(issue, owner) {
		if c.ID > upto {
			upto = c.ID
		}
	}
	return Ack{V: Version, Upto: upto, Run: run}
}

// LastError returns the relay-error marker of the latest error comment, by
// comment id.
func LastError(issue Issue) (ErrorMarker, bool) {
	var found ErrorMarker
	var ok bool
	for _, c := range sortedComments(issue) {
		if c.Author != BotLogin || !IsRelayComment(c.Body) {
			continue
		}
		if e, has, err := ParseError(c.Body); err == nil && has {
			found, ok = e, true
		}
	}
	return found, ok
}

// Reconcile decides the next action from the Issue state alone, not from the
// event that woke the workflow. That is what survives GitHub cancelling
// pending runs in a concurrency group: whichever run gets to execute sees
// every owner comment above the watermark and acts on the latest command
// that is valid for the Issue's state.
//
// When commands are pending but none is valid, the decision is "ignored"
// with the reason for the latest one. In a discussion, "discuss" is added
// whenever a plain reply is pending too, so that an invalid command never
// hides the owner's question.
func Reconcile(issue Issue, owner string) Decision {
	if owner == "" {
		return nothing(issue.Number, "owner is not configured")
	}
	labels, err := ParseLabels(issue.Labels)
	if err != nil {
		return nothing(issue.Number, "labels: "+err.Error())
	}
	if labels.State == "" {
		return nothing(issue.Number, "no state label")
	}

	pending := Pending(issue, owner)
	if len(pending) == 0 {
		return nothing(issue.Number, "nothing new since watermark")
	}
	maxID := pending[len(pending)-1].ID

	var cmds []ParsedCommand
	hasReply := false
	for _, c := range pending {
		if cmd, ok := ParseCommand(c.Body); ok {
			cmds = append(cmds, cmd)
		} else {
			hasReply = true
		}
	}

	for i := len(cmds) - 1; i >= 0; i-- {
		cmd := cmds[i]
		if stages, reason, ok := validate(cmd, labels, issue); ok {
			return Decision{Issue: issue.Number, Stages: stages, Reason: reason, Upto: maxID, Command: &cmd}
		}
	}
	if len(cmds) > 0 {
		latest := cmds[len(cmds)-1]
		_, reason, _ := validate(latest, labels, issue)
		stages := []Stage{StageIgnored}
		upto := maxID
		if labels.State == StateDiscuss && hasReply {
			// discuss runs after the ignored comment and must still see
			// the reply, so the ignored comment does not move the
			// watermark; discuss acknowledges everything when it posts.
			stages = append(stages, StageDiscuss)
			upto = Watermark(issue)
		}
		return Decision{Issue: issue.Number, Stages: stages, Reason: reason, Upto: upto, Command: &latest}
	}

	switch {
	case labels.State == StateDiscuss:
		return Decision{Issue: issue.Number, Stages: []Stage{StageDiscuss}, Reason: "owner replied in discussion", Upto: maxID}
	case labels.NeedsApproval:
		return nothing(issue.Number, "waiting for /approve")
	default:
		return nothing(issue.Number, fmt.Sprintf("no command and not in discussion (%s)", labels.State))
	}
}

// validate says whether cmd can run against the Issue's labels; when it can,
// stages is what to run, otherwise reason says why not.
func validate(cmd ParsedCommand, labels Labels, issue Issue) (stages []Stage, reason string, ok bool) {
	if !cmd.Name.InV0() {
		return nil, fmt.Sprintf("/%s is not available in v0", cmd.Name), false
	}
	switch cmd.Name {
	case CmdTicket:
		if labels.State != StateDiscuss {
			return nil, fmt.Sprintf("/ticket needs relay:discuss, Issue is %s", labels.State), false
		}
		return StagesFrom(StageTicket), "/ticket", true
	case CmdApprove:
		if labels.State != StatePlan || !labels.NeedsApproval {
			return nil, "/approve needs relay:plan with needs-approval", false
		}
		return []Stage{StageApprove}, "/approve", true
	case CmdRetry:
		if labels.State != StateFailed {
			return nil, fmt.Sprintf("/retry needs relay:failed, Issue is %s", labels.State), false
		}
		e, has := LastError(issue)
		if !has {
			return nil, "/retry: no relay-error marker to resume from", false
		}
		stages := StagesFrom(e.Stage)
		if stages == nil || e.Stage == StageDiscuss {
			return nil, fmt.Sprintf("/retry: cannot resume stage %q", e.Stage), false
		}
		return stages, "/retry " + string(e.Stage), true
	}
	return nil, fmt.Sprintf("unhandled command /%s", cmd.Name), false
}
