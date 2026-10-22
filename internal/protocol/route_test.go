package protocol

import (
	"reflect"
	"testing"
)

func TestRoute(t *testing.T) {
	discussIssue := Issue{Number: 7, Labels: []string{"relay:discuss"}, Comments: []Comment{
		ownerSays(100, "todo: dark theme"),
		relayComment(t, 101, StageDiscuss, 100),
		ownerSays(102, "/ticket"),
	}}
	private := func(ev WebhookEvent) WebhookEvent { ev.RepoPrivate = true; return ev }

	tests := []struct {
		name       string
		ev         WebhookEvent
		owner      string
		wantStages []Stage
		wantIssue  int
		wantReason string
	}{
		{
			name:       "public repository: nothing, whatever the event",
			ev:         WebhookEvent{Name: EventIssueComment, Action: "created", Sender: owner, Issue: discussIssue, Comment: &discussIssue.Comments[2]},
			wantStages: nil, wantIssue: 7, wantReason: "repository is not private",
		},
		{
			name:       "no owner configured: nothing",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: owner, Issue: discussIssue}),
			owner:      "-",
			wantStages: nil, wantIssue: 7, wantReason: "owner is not configured",
		},
		{
			name:       "owner comment goes through reconcile",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: owner, Issue: discussIssue, Comment: &discussIssue.Comments[2]}),
			wantStages: []Stage{StageTicket, StageTriage, StagePlan}, wantIssue: 7,
		},
		{
			name:       "sender login is matched case-insensitively",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: "MASTEINE", Issue: discussIssue, Comment: &discussIssue.Comments[2]}),
			wantStages: []Stage{StageTicket, StageTriage, StagePlan}, wantIssue: 7,
		},
		{
			name:       "comment on a pull request",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: owner, IsPullRequest: true, Issue: discussIssue}),
			wantStages: nil, wantIssue: 7, wantReason: "comment on a pull request",
		},
		{
			name:       "comment by the bot never routes (no loops)",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: BotLogin, Issue: discussIssue, Comment: &discussIssue.Comments[1]}),
			wantStages: nil, wantIssue: 7, wantReason: "comment by github-actions[bot]",
		},
		{
			name:       "comment by someone else",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "created", Sender: "stranger", Issue: discussIssue, Comment: &Comment{ID: 103, Author: "stranger", Body: "/ticket"}}),
			wantStages: nil, wantIssue: 7, wantReason: `sender "stranger" is not the owner`,
		},
		{
			name:       "edited comment is not handled",
			ev:         private(WebhookEvent{Name: EventIssueComment, Action: "edited", Sender: owner, Issue: discussIssue}),
			wantStages: nil, wantIssue: 7, wantReason: "issue_comment.edited is not handled",
		},
		{
			name:       "manual start: owner adds `relay` to an issue without state",
			ev:         private(WebhookEvent{Name: EventIssues, Action: "labeled", Sender: owner, Label: "relay", Issue: Issue{Number: 8, Labels: []string{"bug", "relay"}}}),
			wantStages: []Stage{StageTriage, StagePlan}, wantIssue: 8, wantReason: "manual start",
		},
		{
			name:       "bot creating an issue with relay:discuss (S4-T1) does not start anything",
			ev:         private(WebhookEvent{Name: EventIssues, Action: "labeled", Sender: owner, Label: "relay:discuss", Issue: Issue{Number: 9, Labels: []string{"relay:discuss"}}}),
			wantStages: nil, wantIssue: 9, wantReason: `label "relay:discuss" does not start anything`,
		},
		{
			name:       "`relay` added to an issue that already has a state",
			ev:         private(WebhookEvent{Name: EventIssues, Action: "labeled", Sender: owner, Label: "relay", Issue: Issue{Number: 10, Labels: []string{"relay", "relay:plan"}}}),
			wantStages: nil, wantIssue: 10, wantReason: "`relay` added but Issue already has relay:plan",
		},
		{
			name:       "label added by someone else",
			ev:         private(WebhookEvent{Name: EventIssues, Action: "labeled", Sender: "stranger", Label: "relay", Issue: Issue{Number: 8, Labels: []string{"relay"}}}),
			wantStages: nil, wantIssue: 8, wantReason: `sender "stranger" is not the owner`,
		},
		{
			name:       "unlabeled is not handled",
			ev:         private(WebhookEvent{Name: EventIssues, Action: "unlabeled", Sender: owner, Label: "relay", Issue: Issue{Number: 8}}),
			wantStages: nil, wantIssue: 8, wantReason: "issues.unlabeled is not handled",
		},
		{
			name:       "dispatch by owner: plan only",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: owner, Dispatch: &DispatchInputs{Issue: 11, Stage: StagePlan}}),
			wantStages: []Stage{StagePlan}, wantIssue: 11, wantReason: "dispatch plan",
		},
		{
			name:       "dispatch from a job (S4-T5): sender is the bot, issue from inputs",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: BotLogin, Dispatch: &DispatchInputs{Issue: 11, Stage: StageTicket}}),
			wantStages: []Stage{StageTicket, StageTriage, StagePlan}, wantIssue: 11,
		},
		{
			name:       "dispatch stage=discuss runs discuss only",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: owner, Dispatch: &DispatchInputs{Issue: 11, Stage: StageDiscuss}}),
			wantStages: []Stage{StageDiscuss}, wantIssue: 11,
		},
		{
			name:       "dispatch stage=approve is not allowed",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: owner, Dispatch: &DispatchInputs{Issue: 11, Stage: StageApprove}}),
			wantStages: nil, wantIssue: 11, wantReason: `dispatch: stage "approve" cannot be dispatched`,
		},
		{
			name:       "dispatch by a stranger",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: "stranger", Dispatch: &DispatchInputs{Issue: 11, Stage: StagePlan}}),
			wantStages: nil, wantIssue: 0, wantReason: `dispatch by "stranger" is not allowed`,
		},
		{
			name:       "dispatch without issue",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: owner, Dispatch: &DispatchInputs{Stage: StagePlan}}),
			wantStages: nil, wantIssue: 0, wantReason: "dispatch without an issue number",
		},
		{
			name:       "dispatch of a v1 stage",
			ev:         private(WebhookEvent{Name: EventWorkflowDispatch, Sender: owner, Dispatch: &DispatchInputs{Issue: 11, Stage: StageImplement}}),
			wantStages: nil, wantIssue: 11, wantReason: `dispatch: stage "implement" cannot be dispatched`,
		},
		{
			name:       "unknown event",
			ev:         private(WebhookEvent{Name: EventName("push"), Sender: owner, Issue: Issue{Number: 3}}),
			wantStages: nil, wantIssue: 3, wantReason: `event "push" is not handled`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := owner
			if tc.owner == "-" {
				o = ""
			}
			d := Route(tc.ev, o)
			if !reflect.DeepEqual(d.Stages, tc.wantStages) {
				t.Errorf("Stages = %v, want %v (reason %q)", d.Stages, tc.wantStages, d.Reason)
			}
			if d.Issue != tc.wantIssue {
				t.Errorf("Issue = %d, want %d", d.Issue, tc.wantIssue)
			}
			if tc.wantReason != "" && d.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
