package protocol

import (
	"reflect"
	"testing"
)

func TestStagesFrom(t *testing.T) {
	tests := map[Stage][]Stage{
		StageDiscuss:   {StageDiscuss},
		StageTicket:    {StageTicket, StageTriage, StagePlan},
		StageTriage:    {StageTriage, StagePlan},
		StagePlan:      {StagePlan},
		StageApprove:   nil,
		StageImplement: nil,
		StageIgnored:   nil,
		StageError:     nil,
		Stage(""):      nil,
	}
	for in, want := range tests {
		if got := StagesFrom(in); !reflect.DeepEqual(got, want) {
			t.Errorf("StagesFrom(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEntryState(t *testing.T) {
	tests := map[Stage]State{
		StageDiscuss: StateDiscuss, StageTicket: StateDiscuss,
		StageTriage: StateTriage, StagePlan: StatePlan, StageApprove: StatePlan,
		StageImplement: "", StageIgnored: "", StageError: "",
	}
	for in, want := range tests {
		if got := EntryState(in); got != want {
			t.Errorf("EntryState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPipelineStageAndConsumesInput(t *testing.T) {
	pipeline := []Stage{StageDiscuss, StageTicket, StageTriage, StagePlan, StageApprove, StageImplement, StageTest, StageReview, StageDeploy}
	for _, s := range pipeline {
		if !IsPipelineStage(s) {
			t.Errorf("%s should be a pipeline stage", s)
		}
	}
	for _, s := range []Stage{StageIgnored, StageError, ""} {
		if IsPipelineStage(s) {
			t.Errorf("%q should not be a pipeline stage", s)
		}
	}
	consuming := map[Stage]bool{StageDiscuss: true, StageTicket: true, StageApprove: true, StageIgnored: true}
	for _, s := range append(pipeline, StageIgnored, StageError) {
		if got := ConsumesInput(s); got != consuming[s] {
			t.Errorf("ConsumesInput(%s) = %v, want %v", s, got, consuming[s])
		}
	}
}
