package protocol

import (
	"reflect"
	"testing"
)

const owner = "masteine"

// relayComment builds a bot comment with an ack, the way a stage posts it.
func relayComment(t *testing.T, id int64, stage Stage, upto int64, extra ...any) Comment {
	t.Helper()
	c := StageComment{Stage: stage, Body: "…", Ack: Ack{Upto: upto}}
	for _, e := range extra {
		switch v := e.(type) {
		case *ErrorMarker:
			c.Error = v
		case *Session:
			c.Session = v
		}
	}
	body, err := FormatStageComment(c)
	if err != nil {
		t.Fatal(err)
	}
	return Comment{ID: id, Author: BotLogin, Body: body}
}

func ownerSays(id int64, body string) Comment { return Comment{ID: id, Author: owner, Body: body} }

func TestReconcile(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		comments   []Comment
		wantStages []Stage
		wantUpto   int64
		wantReason string
		wantCmd    Command
	}{
		{
			name:       "fresh issue: original request is discussion input",
			labels:     []string{"relay:discuss"},
			comments:   []Comment{ownerSays(100, "todo: dark theme")},
			wantStages: []Stage{StageDiscuss},
			wantUpto:   100,
		},
		{
			name:   "reply after a relay answer",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(102, "yes, follow the system theme"),
			},
			wantStages: []Stage{StageDiscuss},
			wantUpto:   102,
		},
		{
			name:   "nothing new since watermark",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
			},
			wantStages: nil,
			wantReason: "nothing new since watermark",
		},
		{
			name:   "input that arrived during a stage is still pending",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				ownerSays(101, "and the icon too"), // arrived while discuss was running on #100
				relayComment(t, 102, StageDiscuss, 100),
			},
			wantStages: []Stage{StageDiscuss},
			wantUpto:   101,
		},
		{
			name:   "/ticket in discussion",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(102, "/ticket"),
			},
			wantStages: []Stage{StageTicket, StageTriage, StagePlan},
			wantUpto:   102,
			wantCmd:    CmdTicket,
		},
		{
			name:   "/ticket followed by replies (S4-T6): ticket still wins",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(102, "/ticket"),
				ownerSays(103, "reply1"),
				ownerSays(104, "reply2"),
			},
			wantStages: []Stage{StageTicket, StageTriage, StagePlan},
			wantUpto:   104,
			wantCmd:    CmdTicket,
		},
		{
			name:   "latest valid command wins over an earlier one",
			labels: []string{"relay", "relay:plan", "needs-approval"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/retry"),
				ownerSays(112, "/approve"),
			},
			wantStages: []Stage{StageApprove},
			wantUpto:   112,
			wantCmd:    CmdApprove,
		},
		{
			name:   "a valid command is not hidden by a later invalid one",
			labels: []string{"relay", "relay:plan", "needs-approval"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/approve"),
				ownerSays(112, "/ticket"), // by mistake
			},
			wantStages: []Stage{StageApprove},
			wantUpto:   112,
			wantCmd:    CmdApprove,
		},
		{
			name:   "/ticket outside discussion is ignored",
			labels: []string{"relay", "relay:triage"},
			comments: []Comment{
				relayComment(t, 105, StageTicket, 104),
				ownerSays(106, "/ticket"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   106,
			wantReason: "/ticket needs relay:discuss, Issue is relay:triage",
			wantCmd:    CmdTicket,
		},
		{
			name:   "invalid command in discussion does not hide the owner's reply",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(102, "and what about the icon?"),
				ownerSays(103, "/approve"),
			},
			wantStages: []Stage{StageIgnored, StageDiscuss},
			wantUpto:   100, // the old watermark: the ignored comment must not consume the reply
			wantReason: "/approve needs relay:plan with needs-approval",
			wantCmd:    CmdApprove,
		},
		{
			name:   "invalid command alone in discussion: ignored only",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(102, "/approve"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   102,
			wantCmd:    CmdApprove,
		},
		{
			name:   "/approve with needs-approval",
			labels: []string{"relay", "relay:plan", "needs-approval"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/approve"),
			},
			wantStages: []Stage{StageApprove},
			wantUpto:   111,
			wantCmd:    CmdApprove,
		},
		{
			name:   "/approve twice: second is ignored",
			labels: []string{"relay", "relay:plan"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/approve"),
				relayComment(t, 112, StageApprove, 111),
				ownerSays(113, "/approve"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   113,
			wantReason: "/approve needs relay:plan with needs-approval",
			wantCmd:    CmdApprove,
		},
		{
			name:   "/approve outside plan is ignored even with needs-approval",
			labels: []string{"relay:discuss", "needs-approval"},
			comments: []Comment{
				ownerSays(100, "/approve"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   100,
			wantCmd:    CmdApprove,
		},
		{
			name:   "plain reply while waiting for approval does nothing",
			labels: []string{"relay", "relay:plan", "needs-approval"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "looks good but let me think"),
			},
			wantStages: nil,
			wantReason: "waiting for /approve",
		},
		{
			name:   "plain reply on triage does nothing",
			labels: []string{"relay", "relay:triage"},
			comments: []Comment{
				relayComment(t, 105, StageTicket, 104),
				ownerSays(106, "oh, also the icon"),
			},
			wantStages: nil,
			wantReason: "no command and not in discussion (relay:triage)",
		},
		{
			name:   "plain reply on failed does nothing",
			labels: []string{"relay", "relay:failed"},
			comments: []Comment{
				relayComment(t, 108, StageError, 104, &ErrorMarker{Stage: StageTriage}),
				ownerSays(109, "hm"),
			},
			wantStages: nil,
			wantReason: "no command and not in discussion (relay:failed)",
		},
		{
			name:   "/retry resumes the failed stage",
			labels: []string{"relay", "relay:failed"},
			comments: []Comment{
				relayComment(t, 108, StageError, 104, &ErrorMarker{Stage: StageTriage, Reason: "deadline"}),
				ownerSays(109, "/retry"),
			},
			wantStages: []Stage{StageTriage, StagePlan},
			wantUpto:   109,
			wantCmd:    CmdRetry,
		},
		{
			name:   "/retry uses the latest error marker by id, whatever the order",
			labels: []string{"relay", "relay:failed"},
			comments: []Comment{
				relayComment(t, 110, StageError, 109, &ErrorMarker{Stage: StagePlan}),
				ownerSays(111, "/retry"),
				relayComment(t, 108, StageError, 104, &ErrorMarker{Stage: StageTriage}),
				ownerSays(109, "/retry"),
			},
			wantStages: []Stage{StagePlan},
			wantUpto:   111,
			wantCmd:    CmdRetry,
		},
		{
			name:   "/retry without an error marker is ignored",
			labels: []string{"relay", "relay:failed"},
			comments: []Comment{
				ownerSays(109, "/retry"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   109,
			wantReason: "/retry: no relay-error marker to resume from",
			wantCmd:    CmdRetry,
		},
		{
			name:   "/retry of a failed approve is ignored",
			labels: []string{"relay", "relay:failed"},
			comments: []Comment{
				relayComment(t, 108, StageError, 104, &ErrorMarker{Stage: StageApprove}),
				ownerSays(109, "/retry"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   109,
			wantReason: `/retry: cannot resume stage "approve"`,
			wantCmd:    CmdRetry,
		},
		{
			name:   "/retry when not failed is ignored",
			labels: []string{"relay", "relay:plan"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/retry"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   111,
			wantReason: "/retry needs relay:failed, Issue is relay:plan",
			wantCmd:    CmdRetry,
		},
		{
			name:   "v1 command is ignored with a reason",
			labels: []string{"relay", "relay:plan", "needs-approval"},
			comments: []Comment{
				relayComment(t, 110, StagePlan, 104),
				ownerSays(111, "/reject wrong module"),
			},
			wantStages: []Stage{StageIgnored},
			wantUpto:   111,
			wantReason: "/reject is not available in v0",
			wantCmd:    CmdReject,
		},
		{
			name:   "comments by others are not input",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				relayComment(t, 101, StageDiscuss, 100),
				{ID: 102, Author: "someone-else", Body: "/ticket"},
			},
			wantStages: nil,
			wantReason: "nothing new since watermark",
		},
		{
			name:   "owner login is matched case-insensitively",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				{ID: 100, Author: "Masteine", Body: "todo: dark theme"},
			},
			wantStages: []Stage{StageDiscuss},
			wantUpto:   100,
		},
		{
			name:   "a human pasting a Relay heading does not move the watermark",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(100, "todo: dark theme"),
				{ID: 101, Author: "someone-else", Body: "## Relay · discuss\n\n<!-- relay-ack {\"v\":1,\"upto\":100} -->"},
			},
			wantStages: []Stage{StageDiscuss},
			wantUpto:   100,
		},
		{
			name:   "comments out of order are sorted by id",
			labels: []string{"relay:discuss"},
			comments: []Comment{
				ownerSays(102, "/ticket"),
				relayComment(t, 101, StageDiscuss, 100),
				ownerSays(100, "todo: dark theme"),
			},
			wantStages: []Stage{StageTicket, StageTriage, StagePlan},
			wantUpto:   102,
			wantCmd:    CmdTicket,
		},
		{
			name:       "invalid labels: nothing, with the reason",
			labels:     []string{"relay:plan", "relay:triage"},
			comments:   []Comment{ownerSays(100, "/ticket")},
			wantStages: nil,
			wantReason: "labels: more than one state label: relay:plan, relay:triage",
		},
		{
			name:       "no state label: nothing",
			labels:     []string{"bug"},
			comments:   []Comment{ownerSays(100, "/ticket")},
			wantStages: nil,
			wantReason: "no state label",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Reconcile(Issue{Number: 42, Labels: tc.labels, Comments: tc.comments}, owner)
			if d.Issue != 42 {
				t.Errorf("Issue = %d", d.Issue)
			}
			if !reflect.DeepEqual(d.Stages, tc.wantStages) {
				t.Errorf("Stages = %v, want %v (reason %q)", d.Stages, tc.wantStages, d.Reason)
			}
			if d.Upto != tc.wantUpto {
				t.Errorf("Upto = %d, want %d", d.Upto, tc.wantUpto)
			}
			if tc.wantReason != "" && d.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", d.Reason, tc.wantReason)
			}
			switch {
			case tc.wantCmd == "" && d.Command != nil:
				t.Errorf("Command = %+v, want none", d.Command)
			case tc.wantCmd != "" && (d.Command == nil || d.Command.Name != tc.wantCmd):
				t.Errorf("Command = %+v, want %s", d.Command, tc.wantCmd)
			}
			if d.Ignored() && d.Command == nil {
				t.Error("ignored decision must carry the command")
			}
		})
	}
}

// TestIgnoredThenDiscussStillSeesReply plays the two jobs of an
// [ignored, discuss] decision: the route job posts the ignored comment with
// IgnoredAck, then the discuss stage looks at what is pending.
func TestIgnoredThenDiscussStillSeesReply(t *testing.T) {
	issue := Issue{Number: 1, Labels: []string{"relay:discuss"}, Comments: []Comment{
		ownerSays(100, "todo: dark theme"),
		relayComment(t, 101, StageDiscuss, 100),
		ownerSays(102, "and what about the icon?"),
		ownerSays(103, "/approve"),
	}}
	d := Reconcile(issue, owner)
	if !reflect.DeepEqual(d.Stages, []Stage{StageIgnored, StageDiscuss}) {
		t.Fatalf("Stages = %v", d.Stages)
	}
	ignored, err := FormatStageComment(StageComment{Stage: StageIgnored, Body: d.Reason, Ack: d.IgnoredAck("run-7")})
	if err != nil {
		t.Fatal(err)
	}
	issue.Comments = append(issue.Comments, Comment{ID: 104, Author: BotLogin, Body: ignored})

	// The discuss stage starts: the reply (and the command) are still pending.
	p := Pending(issue, owner)
	if len(p) != 2 || p[0].ID != 102 || p[1].ID != 103 {
		t.Fatalf("Pending after ignored = %+v, want #102 and #103", p)
	}
	if a := AckFor(StageDiscuss, issue, owner, ""); a.Upto != 103 {
		t.Errorf("discuss acks %d, want 103", a.Upto)
	}
	// And a plain ignored (no reply pending) consumes what it answered.
	only := Issue{Number: 2, Labels: []string{"relay:discuss"}, Comments: []Comment{
		ownerSays(100, "todo"), relayComment(t, 101, StageDiscuss, 100), ownerSays(102, "/approve"),
	}}
	if d := Reconcile(only, owner); d.IgnoredAck("").Upto != 102 {
		t.Errorf("ignored alone should ack 102, got %d", d.IgnoredAck("").Upto)
	}
}

func TestReconcileRequiresOwner(t *testing.T) {
	d := Reconcile(Issue{Number: 1, Labels: []string{"relay:discuss"}, Comments: []Comment{ownerSays(1, "x")}}, "")
	if d.Stages != nil || d.Reason != "owner is not configured" {
		t.Errorf("got %+v", d)
	}
}

func TestWatermarkAndPending(t *testing.T) {
	issue := Issue{Comments: []Comment{
		ownerSays(1, "x"),
		relayComment(t, 2, StageDiscuss, 1),
		relayComment(t, 5, StageDiscuss, 4),
		relayComment(t, 3, StageDiscuss, 2),
		ownerSays(6, "after"),
		{ID: 7, Author: "stranger", Body: "not input"},
		ownerSays(4, "during"),
	}}
	if got := Watermark(issue); got != 4 {
		t.Errorf("Watermark = %d, want 4", got)
	}
	if got := Watermark(Issue{}); got != 0 {
		t.Errorf("Watermark of empty issue = %d", got)
	}
	p := Pending(issue, owner)
	if len(p) != 1 || p[0].ID != 6 {
		t.Errorf("Pending = %+v, want only #6", p)
	}
}

func TestAckFor(t *testing.T) {
	issue := Issue{Comments: []Comment{
		ownerSays(100, "todo"),
		relayComment(t, 101, StageDiscuss, 100),
		ownerSays(102, "/ticket"),
		ownerSays(103, "reply during the chain"),
	}}
	// Stages that read input consume everything pending.
	for _, s := range []Stage{StageDiscuss, StageTicket, StageApprove, StageIgnored} {
		if a := AckFor(s, issue, owner, "run-1"); a.Upto != 103 || a.V != Version || a.Run != "run-1" {
			t.Errorf("AckFor(%s) = %+v", s, a)
		}
	}
	// Chained stages and the error report repeat the watermark.
	for _, s := range []Stage{StageTriage, StagePlan, StageError} {
		if a := AckFor(s, issue, owner, ""); a.Upto != 100 {
			t.Errorf("AckFor(%s).Upto = %d, want 100", s, a.Upto)
		}
	}
	// A retried stage consumes everything, including the /retry itself.
	if a := AckAll(issue, owner, "r"); a.Upto != 103 || a.Run != "r" {
		t.Errorf("AckAll = %+v", a)
	}
}

func TestStagesJSON(t *testing.T) {
	if got := (Decision{}).StagesJSON(); got != "[]" {
		t.Errorf("empty = %s", got)
	}
	if got := (Decision{Stages: []Stage{StageTicket, StageTriage}}).StagesJSON(); got != `["ticket","triage"]` {
		t.Errorf("got %s", got)
	}
}
