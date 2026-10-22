// Package config reads relay.yml, the per-project configuration. It is short
// on purpose: the preset carries everything heavy, and the file only names
// the owner, the platform, the gates and the model per stage.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/masteine/relay/internal/protocol"
)

// FileName is the configuration file at the repository root.
const FileName = "relay.yml"

// Model is the model and effort a stage runs with.
type Model struct {
	Model  string `yaml:"model"`
	Effort string `yaml:"effort"`
}

// Limits bound what one task may spend.
type Limits struct {
	MaxTurnsPerStage int    `yaml:"max_turns_per_stage"`
	BudgetPerTask    string `yaml:"budget_per_task"` // "off" or a dollar amount; v1
	Concurrency      int    `yaml:"concurrency"`
}

// Project is the parsed relay.yml.
type Project struct {
	Owner    string           `yaml:"owner"`
	Platform string           `yaml:"platform"`
	Preset   string           `yaml:"preset"`
	Test     string           `yaml:"test"`
	Deploy   string           `yaml:"deploy"`
	Design   string           `yaml:"design"`
	Gates    []string         `yaml:"gates"`
	Models   map[string]Model `yaml:"models"`
	Limits   Limits           `yaml:"limits"`
}

var (
	platforms = map[string]bool{"android": true, "web": true, "desktop": true}
	designs   = map[string]bool{"auto": true, "always": true, "never": true}
	gates     = map[string]bool{"design": true, "plan": true, "deploy": true}
	efforts   = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	modelStgs = map[string]bool{"discuss": true, "triage": true, "plan": true, "implement": true, "test": true, "review": true}
)

// Defaults are what a missing field means. Thinking stages (triage, plan,
// review) get the strongest model at high effort; execution stages a lighter
// one. fable is never a default: in headless runs it can bill usage credits
// beyond the subscription without asking.
func Defaults() Project {
	return Project{
		Design: "auto",
		Gates:  []string{"plan"},
		Models: map[string]Model{
			"discuss":   {Model: "sonnet", Effort: "medium"},
			"triage":    {Model: "opus", Effort: "high"},
			"plan":      {Model: "opus", Effort: "high"},
			"implement": {Model: "sonnet", Effort: "medium"},
			"test":      {Model: "sonnet", Effort: "medium"},
			"review":    {Model: "opus", Effort: "high"},
		},
		Limits: Limits{MaxTurnsPerStage: 200, BudgetPerTask: "off", Concurrency: 1},
	}
}

// Load reads and validates the file at path.
func Load(path string) (Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Project{}, err
	}
	return Parse(data)
}

// Parse reads relay.yml from memory, applies defaults and validates.
func Parse(data []byte) (Project, error) {
	p := Defaults()
	var in Project
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&in); err != nil && err.Error() != "EOF" {
		return Project{}, fmt.Errorf("%s: %w", FileName, err)
	}
	p.Owner = strings.TrimSpace(in.Owner)
	p.Platform = in.Platform
	p.Preset = in.Preset
	p.Test = in.Test
	p.Deploy = in.Deploy
	if in.Design != "" {
		p.Design = in.Design
	}
	if in.Gates != nil {
		p.Gates = in.Gates
	}
	for stage, m := range in.Models {
		d := p.Models[stage]
		if m.Model != "" {
			d.Model = m.Model
		}
		if m.Effort != "" {
			d.Effort = m.Effort
		}
		p.Models[stage] = d
	}
	if in.Limits.MaxTurnsPerStage != 0 {
		p.Limits.MaxTurnsPerStage = in.Limits.MaxTurnsPerStage
	}
	if in.Limits.BudgetPerTask != "" {
		p.Limits.BudgetPerTask = in.Limits.BudgetPerTask
	}
	if in.Limits.Concurrency != 0 {
		p.Limits.Concurrency = in.Limits.Concurrency
	}
	if err := p.Validate(); err != nil {
		return Project{}, fmt.Errorf("%s: %w", FileName, err)
	}
	return p, nil
}

// Validate checks every field that a stage relies on.
func (p Project) Validate() error {
	if p.Owner == "" || strings.HasPrefix(p.Owner, "<") {
		return fmt.Errorf("owner is required (the GitHub login that drives the pipeline)")
	}
	if !platforms[p.Platform] {
		return fmt.Errorf("platform %q: want android, web or desktop", p.Platform)
	}
	if p.Preset == "" || strings.HasPrefix(p.Preset, "<") {
		return fmt.Errorf("preset is required")
	}
	if !designs[p.Design] {
		return fmt.Errorf("design %q: want auto, always or never", p.Design)
	}
	for _, g := range p.Gates {
		if !gates[g] {
			return fmt.Errorf("gates: %q is not a gate (design, plan, deploy)", g)
		}
	}
	stages := make([]string, 0, len(p.Models))
	for s := range p.Models {
		stages = append(stages, s)
	}
	sort.Strings(stages)
	for _, s := range stages {
		m := p.Models[s]
		if !modelStgs[s] {
			return fmt.Errorf("models: %q is not a stage", s)
		}
		if m.Model == "" || strings.HasPrefix(m.Model, "<") {
			return fmt.Errorf("models.%s.model is required", s)
		}
		if !efforts[m.Effort] {
			return fmt.Errorf("models.%s.effort %q: want low, medium, high, xhigh or max", s, m.Effort)
		}
	}
	if p.Limits.MaxTurnsPerStage < 1 {
		return fmt.Errorf("limits.max_turns_per_stage must be positive")
	}
	if p.Limits.Concurrency < 1 {
		return fmt.Errorf("limits.concurrency must be positive")
	}
	return nil
}

// ModelFor returns the model and effort for a stage. Stages without their
// own entry (ticket runs on the discuss model) map to the closest one.
func (p Project) ModelFor(stage protocol.Stage) Model {
	key := string(stage)
	if stage == protocol.StageTicket {
		key = "discuss"
	}
	if m, ok := p.Models[key]; ok {
		return m
	}
	return Model{Model: "sonnet", Effort: "medium"}
}

// HasGate reports whether the pipeline stops before the named gate.
func (p Project) HasGate(gate string) bool {
	for _, g := range p.Gates {
		if g == gate {
			return true
		}
	}
	return false
}
