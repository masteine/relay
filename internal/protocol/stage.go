package protocol

// Stage names a pipeline stage. The same names are used for workflow jobs,
// stage comment headings, plugin skills and the `stages` output of route.
type Stage string

const (
	StageDiscuss Stage = "discuss"
	StageTicket  Stage = "ticket"
	StageTriage  Stage = "triage"
	StagePlan    Stage = "plan"
	StageApprove Stage = "approve"

	// v1 stages. They are named so that markers and labels parse, but no
	// transition or chain in v0 produces them.
	StageImplement Stage = "implement"
	StageTest      Stage = "test"
	StageReview    Stage = "review"
	StageDeploy    Stage = "deploy"

	// Headings that are not pipeline stages but use the same comment format.
	StageIgnored Stage = "ignored"
	StageError   Stage = "error"
)

// chainV0 is the order of stages that run in one workflow run after /ticket.
var chainV0 = []Stage{StageTicket, StageTriage, StagePlan}

// StagesFrom returns the stages to run when resuming at s: s and everything
// after it up to the next gate. Only discuss and the v0 chain can be resumed;
// approve is driven by /approve alone, so it and every other stage return nil.
func StagesFrom(s Stage) []Stage {
	if s == StageDiscuss {
		return []Stage{StageDiscuss}
	}
	for i, c := range chainV0 {
		if c == s {
			out := make([]Stage, len(chainV0)-i)
			copy(out, chainV0[i:])
			return out
		}
	}
	return nil
}

// EntryState is the state label an Issue carries while stage s runs.
// It returns "" for stages that are not part of the v0 pipeline.
func EntryState(s Stage) State {
	switch s {
	case StageDiscuss, StageTicket:
		return StateDiscuss
	case StageTriage:
		return StateTriage
	case StagePlan, StageApprove:
		return StatePlan
	}
	return ""
}

// IsPipelineStage reports whether s is a stage a job runs, as opposed to a
// heading such as "ignored" or "error".
func IsPipelineStage(s Stage) bool {
	switch s {
	case StageDiscuss, StageTicket, StageTriage, StagePlan, StageApprove,
		StageImplement, StageTest, StageReview, StageDeploy:
		return true
	}
	return false
}

// ConsumesInput reports whether a comment of stage s acknowledges the owner's
// pending comments. Stages that read the owner's input (discuss, ticket,
// approve) and the "ignored" reply do; chained stages (triage, plan) and the
// error report do not, so that a comment the owner writes while the chain
// runs is still pending for the next run. See AckFor.
func ConsumesInput(s Stage) bool {
	switch s {
	case StageDiscuss, StageTicket, StageApprove, StageIgnored:
		return true
	}
	return false
}
