package protocol

import "fmt"

// EventName is the GitHub event that woke the workflow.
type EventName string

const (
	EventIssueComment     EventName = "issue_comment"
	EventIssues           EventName = "issues"
	EventWorkflowDispatch EventName = "workflow_dispatch"
)

// DispatchInputs are the inputs of a workflow_dispatch run.
type DispatchInputs struct {
	Issue int
	Stage Stage
}

// WebhookEvent is the part of a workflow event that route needs. The wrapper
// builds it from the event payload and a fresh fetch of the Issue.
type WebhookEvent struct {
	Name        EventName
	Action      string
	Sender      string
	RepoPrivate bool

	// Label is the label added, for issues.labeled.
	Label string
	// IsPullRequest is true for an issue_comment on a pull request.
	IsPullRequest bool
	// Comment is the comment that triggered the run, for issue_comment.
	Comment *Comment
	// Issue is the Issue with all its comments, for issue_comment and issues.
	Issue Issue
	// Dispatch holds the inputs of a workflow_dispatch run.
	Dispatch *DispatchInputs
}

// Route maps an event to the stages to run, in this order of checks:
//
//   - no owner configured, public repository, pull request comment, bot
//     comment, or a sender who is not the owner → nothing;
//   - issues.labeled with `relay` by the owner on an Issue without a state
//     label → manual start (triage, plan); any other label event → nothing;
//   - workflow_dispatch by the owner or the bot → the given stage (discuss,
//     ticket, triage or plan) and the rest of its chain;
//   - issue_comment by the owner → Reconcile.
//
// Logins are compared case-insensitively, as GitHub does.
func Route(ev WebhookEvent, owner string) Decision {
	if owner == "" {
		return nothing(ev.Issue.Number, "owner is not configured")
	}
	if !ev.RepoPrivate {
		return nothing(ev.Issue.Number, "repository is not private")
	}
	switch ev.Name {
	case EventIssueComment:
		if ev.Action != "created" {
			return nothing(ev.Issue.Number, "issue_comment."+ev.Action+" is not handled")
		}
		if ev.IsPullRequest {
			return nothing(ev.Issue.Number, "comment on a pull request")
		}
		if ev.Comment != nil && ev.Comment.Author == BotLogin {
			return nothing(ev.Issue.Number, "comment by "+BotLogin)
		}
		if !sameLogin(ev.Sender, owner) {
			return nothing(ev.Issue.Number, fmt.Sprintf("sender %q is not the owner", ev.Sender))
		}
		return Reconcile(ev.Issue, owner)

	case EventIssues:
		if ev.Action != "labeled" {
			return nothing(ev.Issue.Number, "issues."+ev.Action+" is not handled")
		}
		if !sameLogin(ev.Sender, owner) {
			return nothing(ev.Issue.Number, fmt.Sprintf("sender %q is not the owner", ev.Sender))
		}
		if ev.Label != LabelPipeline {
			return nothing(ev.Issue.Number, fmt.Sprintf("label %q does not start anything", ev.Label))
		}
		labels, err := ParseLabels(ev.Issue.Labels)
		if err != nil {
			return nothing(ev.Issue.Number, "labels: "+err.Error())
		}
		if labels.State != "" {
			return nothing(ev.Issue.Number, fmt.Sprintf("`relay` added but Issue already has %s", labels.State))
		}
		return Decision{Issue: ev.Issue.Number, Stages: StagesFrom(StageTriage), Reason: "manual start"}

	case EventWorkflowDispatch:
		if !sameLogin(ev.Sender, owner) && ev.Sender != BotLogin {
			return nothing(0, fmt.Sprintf("dispatch by %q is not allowed", ev.Sender))
		}
		if ev.Dispatch == nil || ev.Dispatch.Issue == 0 {
			return nothing(0, "dispatch without an issue number")
		}
		stages := StagesFrom(ev.Dispatch.Stage)
		if stages == nil {
			return nothing(ev.Dispatch.Issue, fmt.Sprintf("dispatch: stage %q cannot be dispatched", ev.Dispatch.Stage))
		}
		return Decision{Issue: ev.Dispatch.Issue, Stages: stages, Reason: "dispatch " + string(ev.Dispatch.Stage)}
	}
	return nothing(ev.Issue.Number, fmt.Sprintf("event %q is not handled", ev.Name))
}
