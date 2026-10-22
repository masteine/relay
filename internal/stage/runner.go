// Package stage is `relay stage <name>`: the Go wrapper that runs one
// pipeline stage inside a GitHub Actions job. It fetches the Issue, writes
// the context file, runs Claude Code headlessly, and publishes the result as
// a stage comment plus a label transition. The rules it follows live in
// package protocol; this package only does I/O.
package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/masteine/relay/internal/config"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// Runner holds what every stage needs.
type Runner struct {
	Repo   gh.Repo
	GH     gh.Client
	Claude *Claude
	Config config.Project
	// Dir is the workspace: the repository checkout.
	Dir string
	// RunID identifies the run in markers: GITHUB_RUN_ID.GITHUB_RUN_ATTEMPT.
	RunID string
	// Log receives progress lines; nil means stderr.
	Log io.Writer
	// Git runs git in dir and returns its combined output. nil means exec.
	Git func(ctx context.Context, dir string, args ...string) (string, error)
}

// ErrStage is returned by a stage that ran but failed; the error comment is
// already posted and the labels already moved. The job still exits non-zero
// so that the chain stops.
var ErrStage = errors.New("stage failed")

// ErrSkip is returned when a stage finds the Issue in a state it does not
// apply to (a dispatch of plan on an Issue that waits for approval, say).
// Nothing is posted, and the job exits zero so that report-failure does not
// turn a healthy Issue into a failed one.
var ErrSkip = errors.New("stage skipped")

// deadlineMargin is what a stage keeps for its own API calls after Claude.
const deadlineMargin = 90 * time.Second

func (r *Runner) logf(format string, args ...any) {
	w := r.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "relay: "+format+"\n", args...)
}

// fetch returns the Issue fresh, with its parsed labels.
func (r *Runner) fetch(ctx context.Context, n int) (gh.Issue, protocol.Labels, error) {
	issue, err := r.GH.Issue(ctx, n)
	if err != nil {
		return gh.Issue{}, protocol.Labels{}, err
	}
	labels, err := protocol.ParseLabels(issue.Labels)
	if err != nil {
		return issue, labels, fmt.Errorf("#%d labels: %w", n, err)
	}
	return issue, labels, nil
}

// post publishes a stage comment.
func (r *Runner) post(ctx context.Context, n int, c protocol.StageComment) (int64, error) {
	body, err := protocol.FormatStageComment(c)
	if err != nil {
		return 0, err
	}
	id, err := r.GH.CreateComment(ctx, n, body)
	if err != nil {
		return 0, err
	}
	r.logf("#%d: posted %s (comment %d)", n, protocol.Heading(c.Stage), id)
	return id, nil
}

// transition applies tr to the Issue's current labels.
func (r *Runner) transition(ctx context.Context, n int, tr protocol.Transition, stage protocol.Stage) (protocol.Labels, error) {
	_, cur, err := r.fetch(ctx, n)
	if err != nil {
		return cur, err
	}
	next, err := protocol.Apply(cur, tr, stage)
	if err != nil {
		return cur, err
	}
	if err := gh.ApplyLabels(ctx, r.GH, n, cur, next); err != nil {
		return cur, err
	}
	r.logf("#%d: labels %v → %v (%s)", n, cur.Strings(), next.Strings(), tr)
	return next, nil
}

// enter checks that the Issue is in the state stage starts from and applies
// the transitions that start a stage: /retry (relay:failed with this stage
// in the last relay-error) and the manual start of triage. retried tells
// the caller to acknowledge all pending input.
func (r *Runner) enter(ctx context.Context, n int, stage protocol.Stage) (issue gh.Issue, labels protocol.Labels, retried bool, err error) {
	issue, labels, err = r.fetch(ctx, n)
	if err != nil {
		return
	}
	switch {
	case labels.State == protocol.StateFailed:
		e, ok := protocol.LastError(issue.Protocol())
		if !ok || e.Stage != stage {
			err = fmt.Errorf("%w: #%d is relay:failed at %q, not at %s", ErrSkip, n, e.Stage, stage)
			return
		}
		if labels, err = r.transition(ctx, n, protocol.TransitionRetry, stage); err != nil {
			return
		}
		retried = true
	case labels.State == "" && labels.Pipeline && stage == protocol.StageTriage:
		if labels, err = r.transition(ctx, n, protocol.TransitionManualStart, stage); err != nil {
			return
		}
	}
	if want := protocol.EntryState(stage); labels.State != want {
		err = fmt.Errorf("%w: #%d is %s, %s needs %s", ErrSkip, n, labels.State, stage, want)
		return
	}
	if stage == protocol.StagePlan && labels.NeedsApproval {
		err = fmt.Errorf("%w: #%d already waits for approval", ErrSkip, n)
		return
	}
	return
}

// fail posts the error comment and moves the Issue to relay:failed (or
// leaves the labels alone for discuss and approve, where the owner simply
// writes again). usage may be nil when Claude never ran. The comment goes
// first: it is the information; report-failure repairs the labels if the
// transition fails after it.
func (r *Runner) fail(ctx context.Context, n int, stage protocol.Stage, reason string, usage *protocol.Usage) error {
	issue, _, err := r.fetch(ctx, n)
	if err != nil {
		return err
	}
	reason = shorten(reason, 200)
	var body string
	switch stage {
	case protocol.StageDiscuss:
		body = fmt.Sprintf("Відповідь у дискусії не вдалася: %s\n\nНапишіть ще раз, і я спробую знову.", reason)
	case protocol.StageApprove:
		body = fmt.Sprintf("Підтвердження не вдалося: %s\n\nНапишіть `/approve` ще раз.", reason)
	default:
		body = fmt.Sprintf("Етап **%s** не завершився: %s\n\nНапишіть `/retry`, щоб повторити з цього етапу, або натисніть 🔁 у Telegram.", stage, reason)
	}
	c := protocol.StageComment{
		Stage: protocol.StageError,
		Body:  body,
		Ack:   protocol.AckFor(protocol.StageError, issue.Protocol(), r.Config.Owner, r.RunID),
		Usage: usage,
		Error: &protocol.ErrorMarker{Stage: stage, Reason: reason, Run: r.RunID},
	}
	if _, err := r.post(ctx, n, c); err != nil {
		return err
	}
	if _, err := r.transition(ctx, n, protocol.TransitionFailed, stage); err != nil {
		return err
	}
	return nil
}

// report is what every stage does with a Claude-side failure: post the
// error, move the labels, return ErrStage.
func (r *Runner) report(ctx context.Context, n int, stage protocol.Stage, rerr error, usage *protocol.Usage) error {
	if ferr := r.fail(ctx, n, stage, rerr.Error(), usage); ferr != nil {
		return ferr
	}
	return ErrStage
}

// invoke writes the context file, runs Claude for the stage and returns the
// parsed result and its usage. The deadline is taken from ctx: whatever is
// left, minus a margin for the API calls that follow. A Claude-side failure
// (is_error, deadline) comes back as res.Err() != nil with the usage filled
// in; the caller decides how to report it.
func (r *Runner) invoke(ctx context.Context, stage protocol.Stage, cx Context, tweak func(*Invocation)) (Result, protocol.Usage, error) {
	rel, err := cx.Write(r.Dir)
	if err != nil {
		return Result{}, protocol.Usage{}, fmt.Errorf("write context: %w", err)
	}
	schema, err := Schema(stage)
	if err != nil {
		return Result{}, protocol.Usage{}, err
	}
	m := r.Config.ModelFor(stage)
	inv := Invocation{
		Stage:    stage,
		Prompt:   fmt.Sprintf("/relay:%s %s", stage, filepath.ToSlash(rel)),
		Model:    m.Model,
		Effort:   m.Effort,
		MaxTurns: r.Config.Limits.MaxTurnsPerStage,
		Allowed:  AllowedTools(stage),
		Schema:   schema,
	}
	if dl, ok := ctx.Deadline(); ok {
		inv.Deadline = time.Until(dl) - deadlineMargin
		if inv.Deadline < 30*time.Second {
			inv.Deadline = 30 * time.Second
		}
	}
	if tweak != nil {
		tweak(&inv)
	}
	r.logf("#%d: running %s on %s/%s (max %d turns, deadline %s)", cx.Issue.Number, stage, inv.Model, inv.Effort, inv.MaxTurns, inv.Deadline.Round(time.Second))
	if m.Model == "fable" {
		r.logf("#%d: NOTE fable in headless mode can bill usage credits beyond the subscription", cx.Issue.Number)
	}
	start := time.Now()
	res, err := r.Claude.Run(ctx, inv)
	if err != nil {
		return res, protocol.Usage{}, err
	}
	usage := res.Usage(stage)
	if usage.Ms == 0 {
		usage.Ms = time.Since(start).Milliseconds()
	}
	r.logf("#%d: %s done in %s, %d turns, ≈$%.2f, is_error=%v, denials=%d", cx.Issue.Number, stage, time.Since(start).Round(time.Second), res.NumTurns, res.TotalCostUSD, res.IsError, len(res.Denials))
	return res, usage, nil
}

// output decodes the structured output into v. When Claude returned no
// structured output, the plain result text is tried as JSON.
func output(res Result, v any) error {
	raw := res.StructuredOutput
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(res.Text)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("stage output does not match its schema: %w", err)
	}
	return nil
}

// context builds the Context for an Issue.
func (r *Runner) context(issue gh.Issue) Context {
	return Context{
		Repo:     r.Repo,
		Issue:    issue,
		Branch:   protocol.BranchName(issue.Number, issue.Title),
		SpecPath: protocol.SpecPath(issue.Number, issue.Title),
		Design:   r.Config.Design,
		Owner:    r.Config.Owner,
	}
}

// stageContext bounds the Claude part of a stage. GitHub calls after Claude
// use the parent context, so that a stage that ran out of time can still
// report it.
func stageContext(ctx context.Context, deadline time.Duration) (context.Context, context.CancelFunc) {
	if deadline <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, deadline)
}
