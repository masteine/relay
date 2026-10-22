package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masteine/relay/internal/protocol"
)

const minimal = `
owner: masteine
platform: android
preset: android-release
`

func TestParseDefaults(t *testing.T) {
	p, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if p.Design != "auto" || len(p.Gates) != 1 || p.Gates[0] != "plan" {
		t.Errorf("defaults: design=%q gates=%v", p.Design, p.Gates)
	}
	if m := p.ModelFor(protocol.StagePlan); m.Model != "opus" || m.Effort != "high" {
		t.Errorf("plan model = %+v", m)
	}
	if m := p.ModelFor(protocol.StageTicket); m.Model != "sonnet" || m.Effort != "medium" {
		t.Errorf("ticket model (via discuss) = %+v", m)
	}
	if m := p.ModelFor(protocol.StageApprove); m.Model != "sonnet" {
		t.Errorf("unknown stage falls back to sonnet, got %+v", m)
	}
	if p.Limits.MaxTurnsPerStage != 200 || p.Limits.Concurrency != 1 || p.Limits.BudgetPerTask != "off" {
		t.Errorf("limits = %+v", p.Limits)
	}
	if !p.HasGate("plan") || p.HasGate("deploy") {
		t.Error("HasGate")
	}
}

func TestParseOverrides(t *testing.T) {
	p, err := Parse([]byte(minimal + `
design: never
gates: [design, plan, deploy]
test: ./gradlew test
models:
  plan: { model: fable }
  triage: { effort: medium }
limits:
  max_turns_per_stage: 50
`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Design != "never" || len(p.Gates) != 3 || p.Test != "./gradlew test" {
		t.Errorf("got design=%q gates=%v test=%q", p.Design, p.Gates, p.Test)
	}
	// A partial override keeps the default for the other field.
	if m := p.ModelFor(protocol.StagePlan); m.Model != "fable" || m.Effort != "high" {
		t.Errorf("plan = %+v", m)
	}
	if m := p.ModelFor(protocol.StageTriage); m.Model != "opus" || m.Effort != "medium" {
		t.Errorf("triage = %+v", m)
	}
	if p.Limits.MaxTurnsPerStage != 50 || p.Limits.Concurrency != 1 {
		t.Errorf("limits = %+v", p.Limits)
	}
}

func TestParseTemplateIsValidOnceFilled(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugin", "templates", "relay.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// The template as shipped has placeholders and must be rejected...
	if _, err := Parse(raw); err == nil {
		t.Error("unfilled template should not validate")
	}
	// ...and must validate once /relay:init fills them.
	filled := strings.NewReplacer("<owner>", "masteine", "<platform>", "android", "<preset>", "android-release", "<thinking>", "opus").Replace(string(raw))
	p, err := Parse([]byte(filled))
	if err != nil {
		t.Fatalf("filled template: %v", err)
	}
	if p.Owner != "masteine" || p.ModelFor(protocol.StageTriage).Model != "opus" || p.ModelFor(protocol.StageDiscuss).Model != "sonnet" {
		t.Errorf("filled template parsed as %+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"missing owner", "platform: android\npreset: x\n", "owner is required"},
		{"bad platform", "owner: a\nplatform: ios\npreset: x\n", `platform "ios"`},
		{"bad design", minimal + "design: maybe\n", `design "maybe"`},
		{"bad gate", minimal + "gates: [merge]\n", `"merge" is not a gate`},
		{"bad stage", minimal + "models:\n  deploy: {model: opus, effort: high}\n", `"deploy" is not a stage`},
		{"bad effort", minimal + "models:\n  plan: {effort: extreme}\n", `effort "extreme"`},
		{"unknown field", minimal + "modles: {}\n", "field modles not found"},
		{"zero turns", minimal + "limits: {max_turns_per_stage: -1}\n", "max_turns_per_stage"},
		{"not yaml", "owner: [\n", FileName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), FileName)); err == nil {
		t.Error("want error for a missing file")
	}
}
