package stage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/masteine/relay/internal/protocol"
)

// Route is the `route` job: it decides what the run does and writes the
// `stages` and `issue` outputs. When the decision is "ignored", it posts the
// ignored comment itself, so that the owner gets an answer even though no
// stage job runs.
func (r *Runner) Route(ctx context.Context, eventName string, payload []byte, outputs io.Writer) (protocol.Decision, error) {
	ev, err := ParseEvent(eventName, payload)
	if err != nil {
		return protocol.Decision{}, err
	}
	private, err := r.GH.IsPrivate(ctx)
	if err != nil {
		return protocol.Decision{}, err
	}
	ev.RepoPrivate = ev.RepoPrivate && private

	var issue protocol.Issue
	if ev.IssueNumber != 0 {
		full, err := r.GH.Issue(ctx, ev.IssueNumber)
		if err != nil {
			return protocol.Decision{}, err
		}
		issue = full.Protocol()
		// A comment that is in the payload but not yet in the list (API
		// lag) still counts: the Issue fetch is the source of truth, the
		// payload is the fallback.
		if ev.Comment != nil && !hasComment(issue, ev.Comment.ID) {
			issue.Comments = append(issue.Comments, *ev.Comment)
		}
	}
	d := protocol.Route(ev.Webhook(issue), r.Config.Owner)
	r.logf("route: %s.%s by %s → %s (%s)", ev.Name, ev.Action, ev.Sender, d.StagesJSON(), d.Reason)

	if outputs != nil {
		fmt.Fprintf(outputs, "stages=%s\nissue=%d\n", d.StagesJSON(), d.Issue)
	}
	if d.Ignored() {
		body := d.Reason
		if d.Command != nil {
			body = fmt.Sprintf("`/%s` проігноровано: %s", d.Command.Name, d.Reason)
		}
		_, err := r.post(ctx, d.Issue, protocol.StageComment{Stage: protocol.StageIgnored, Body: body, Ack: d.IgnoredAck(r.RunID)})
		if err != nil {
			return d, err
		}
	}
	return d, nil
}

func hasComment(issue protocol.Issue, id int64) bool {
	for _, c := range issue.Comments {
		if c.ID == id {
			return true
		}
	}
	return false
}

// discussOut is the discuss stage's structured output.
type discussOut struct {
	Body           string `json:"body"`
	ReadyForTicket bool   `json:"ready_for_ticket"`
}

// sessionTaken recognises the CLI's complaint about a --session-id that
// already has a transcript; the message is matched against the result text
// and stderr together.
var sessionTaken = regexp.MustCompile(`(?i)session[^\n]*(already|in use|exists)`)

// sessionGone recognises a --resume of a session this machine does not have.
var sessionGone = regexp.MustCompile(`(?i)no conversation found`)

func (r Result) mentions(re *regexp.Regexp) bool {
	return re.MatchString(r.Text) || re.MatchString(r.Stderr)
}

// Discuss answers everything the owner wrote since the last reply.
//
// Sessions: the first turn creates one with a deterministic id; later turns
// resume it. Two things can go wrong, and each gets one retry within the
// stage's deadline: the deterministic id already exists (an earlier first
// turn died after creating it) → resume it; the recorded session cannot be
// resumed (another machine, a cleaned ~/.claude) → start a fresh one, since
// the context file carries the whole history anyway.
func (r *Runner) Discuss(ctx context.Context, n int, deadline time.Duration) error {
	issue, _, retried, err := r.enter(ctx, n, protocol.StageDiscuss)
	if err != nil {
		return err
	}
	pi := issue.Protocol()
	ack := protocol.AckFor(protocol.StageDiscuss, pi, r.Config.Owner, r.RunID)
	if retried {
		ack = protocol.AckAll(pi, r.Config.Owner, r.RunID)
	}
	cctx, cancel := stageContext(ctx, deadline)
	defer cancel()

	sid := lastSession(pi)
	resume := sid != ""
	if !resume {
		sid = uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("relay:%s#%d", r.Repo, n))).String()
	}
	run := func(resume bool, id string) (Result, protocol.Usage, error) {
		return r.invoke(cctx, protocol.StageDiscuss, r.context(issue), func(inv *Invocation) {
			if resume {
				inv.Resume = id
			} else {
				inv.SessionID = id
			}
		})
	}
	res, usage, err := run(resume, sid)
	if err != nil {
		return err
	}
	first := res.Err()
	switch {
	case first == nil || res.TimedOut:
	case !resume && res.mentions(sessionTaken):
		r.logf("#%d: session %s is taken (%v); resuming it", n, sid, first)
		res, usage, err = run(true, sid)
	case resume && res.mentions(sessionGone):
		fresh := uuid.New().String()
		r.logf("#%d: cannot resume %s (%v); starting a fresh session %s", n, sid, first, fresh)
		sid = fresh
		res, usage, err = run(false, fresh)
	}
	if err != nil {
		return err
	}
	if rerr := res.Err(); rerr != nil {
		if first != nil {
			rerr = first
		}
		return r.report(ctx, n, protocol.StageDiscuss, rerr, &usage)
	}
	var out discussOut
	if err := output(res, &out); err != nil {
		out.Body = strings.TrimSpace(res.Text)
		if out.Body == "" {
			return err
		}
		r.logf("#%d: no structured output, using the result text", n)
	}
	r.logf("#%d: ready_for_ticket=%v", n, out.ReadyForTicket)
	if res.SessionID != "" {
		sid = res.SessionID
	}
	_, err = r.post(ctx, n, protocol.StageComment{
		Stage:   protocol.StageDiscuss,
		Body:    out.Body,
		Ack:     ack,
		Usage:   &usage,
		Session: &protocol.Session{ID: sid},
	})
	return err
}

func lastSession(issue protocol.Issue) string {
	var sid string
	for _, c := range issue.Comments {
		if c.Author != protocol.BotLogin {
			continue
		}
		if s, ok, err := protocol.ParseSession(c.Body); err == nil && ok {
			sid = s.ID
		}
	}
	return sid
}

// ticketOut is the ticket stage's structured output.
type ticketOut struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Ticket writes the task statement into the Issue and moves it into the
// pipeline. Order: rewrite the Issue, post the comment, then the labels; if
// a later step fails, /ticket is either still pending or can be typed again.
func (r *Runner) Ticket(ctx context.Context, n int, deadline time.Duration) error {
	issue, _, _, err := r.enter(ctx, n, protocol.StageTicket)
	if err != nil {
		return err
	}
	ack := protocol.AckAll(issue.Protocol(), r.Config.Owner, r.RunID)
	cctx, cancel := stageContext(ctx, deadline)
	defer cancel()
	res, usage, err := r.invoke(cctx, protocol.StageTicket, r.context(issue), nil)
	if err != nil {
		return err
	}
	if rerr := res.Err(); rerr != nil {
		return r.report(ctx, n, protocol.StageTicket, rerr, &usage)
	}
	var out ticketOut
	if err := output(res, &out); err != nil {
		return r.report(ctx, n, protocol.StageTicket, err, &usage)
	}
	if strings.TrimSpace(out.Body) == "" {
		return r.report(ctx, n, protocol.StageTicket, fmt.Errorf("ticket stage returned an empty body"), &usage)
	}
	if err := r.GH.EditIssue(ctx, n, strings.TrimSpace(out.Title), out.Body); err != nil {
		return err
	}
	r.logf("#%d: body replaced with the task statement", n)
	if _, err := r.post(ctx, n, protocol.StageComment{
		Stage: protocol.StageTicket,
		Body:  "Задачу записано в тіло Issue. Далі: тріаж і план.",
		Ack:   ack,
		Usage: &usage,
	}); err != nil {
		return err
	}
	_, err = r.transition(ctx, n, protocol.TransitionTicket, protocol.StageTicket)
	return err
}

// triageOut is the triage stage's structured output.
type triageOut struct {
	Body        string   `json:"body"`
	Size        string   `json:"size"`
	NeedsDesign bool     `json:"needs_design"`
	KeyFiles    []string `json:"key_files"`
}

// Triage sizes the task and names the files the plan starts from.
func (r *Runner) Triage(ctx context.Context, n int, deadline time.Duration) error {
	issue, _, retried, err := r.enter(ctx, n, protocol.StageTriage)
	if err != nil {
		return err
	}
	ack := protocol.AckFor(protocol.StageTriage, issue.Protocol(), r.Config.Owner, r.RunID)
	if retried {
		ack = protocol.AckAll(issue.Protocol(), r.Config.Owner, r.RunID)
	}
	cctx, cancel := stageContext(ctx, deadline)
	defer cancel()
	res, usage, err := r.invoke(cctx, protocol.StageTriage, r.context(issue), nil)
	if err != nil {
		return err
	}
	if rerr := res.Err(); rerr != nil {
		return r.report(ctx, n, protocol.StageTriage, rerr, &usage)
	}
	var out triageOut
	if err := output(res, &out); err != nil {
		out.Body = strings.TrimSpace(res.Text)
		if out.Body == "" {
			return r.report(ctx, n, protocol.StageTriage, err, &usage)
		}
		r.logf("#%d: no structured output, using the result text", n)
	}
	r.logf("#%d: size %s, needs_design=%v, %d key files", n, out.Size, out.NeedsDesign, len(out.KeyFiles))
	if _, err := r.post(ctx, n, protocol.StageComment{Stage: protocol.StageTriage, Body: out.Body, Ack: ack, Usage: &usage}); err != nil {
		return err
	}
	_, err = r.transition(ctx, n, protocol.TransitionTriageDone, protocol.StageTriage)
	return err
}

// planOut is the plan stage's structured output.
type planOut struct {
	Body       string `json:"body"`
	SpecPath   string `json:"spec_path"`
	Verdict    string `json:"verdict"`
	Iterations int    `json:"iterations"`
}

// Plan runs the planning loop, commits the spec to the task branch and asks
// for approval.
func (r *Runner) Plan(ctx context.Context, n int, deadline time.Duration) error {
	issue, _, retried, err := r.enter(ctx, n, protocol.StagePlan)
	if err != nil {
		return err
	}
	cx := r.context(issue)
	ack := protocol.AckFor(protocol.StagePlan, issue.Protocol(), r.Config.Owner, r.RunID)
	if retried {
		ack = protocol.AckAll(issue.Protocol(), r.Config.Owner, r.RunID)
	}
	cctx, cancel := stageContext(ctx, deadline)
	defer cancel()
	res, usage, err := r.invoke(cctx, protocol.StagePlan, cx, nil)
	if err != nil {
		return err
	}
	if rerr := res.Err(); rerr != nil {
		return r.report(ctx, n, protocol.StagePlan, rerr, &usage)
	}
	var out planOut
	if err := output(res, &out); err != nil {
		return r.report(ctx, n, protocol.StagePlan, err, &usage)
	}
	if filepath.Clean(out.SpecPath) != cx.SpecPath {
		return r.report(ctx, n, protocol.StagePlan, fmt.Errorf("the plan stage wrote %q instead of %s", out.SpecPath, cx.SpecPath), &usage)
	}
	if st, err := os.Lstat(filepath.Join(r.Dir, cx.SpecPath)); err != nil || !st.Mode().IsRegular() {
		return r.report(ctx, n, protocol.StagePlan, fmt.Errorf("the plan stage did not write %s", cx.SpecPath), &usage)
	}
	msg := fmt.Sprintf("Add spec for #%d (%s)\n\nRelay plan stage, verdict %s after %d round(s).", n, out.Verdict, out.Verdict, out.Iterations)
	if err := r.commitSpec(ctx, cx.Branch, cx.SpecPath, msg); err != nil {
		return r.report(ctx, n, protocol.StagePlan, fmt.Errorf("could not commit the spec: %w", err), &usage)
	}
	body := out.Body
	if out.Verdict == "NO_CONSENSUS" && !strings.Contains(body, "NO_CONSENSUS") {
		body += "\n\n⚠️ Консенсусу planner → architect → critic не досягнуто за 3 раунди; план опубліковано як є."
	}
	if _, err := r.post(ctx, n, protocol.StageComment{Stage: protocol.StagePlan, Body: body, Ack: ack, Usage: &usage}); err != nil {
		return err
	}
	_, err = r.transition(ctx, n, protocol.TransitionPlanDone, protocol.StagePlan)
	return err
}

// Approve clears needs-approval. In v0 the pipeline ends here. The comment
// goes first; if the labels then fail, /approve can simply be typed again.
func (r *Runner) Approve(ctx context.Context, n int) error {
	issue, labels, err := r.fetch(ctx, n)
	if err != nil {
		return err
	}
	if labels.State != protocol.StatePlan || !labels.NeedsApproval {
		return fmt.Errorf("%w: #%d is [%s], nothing to approve", ErrSkip, n, strings.Join(labels.Strings(), " "))
	}
	ack := protocol.AckFor(protocol.StageApprove, issue.Protocol(), r.Config.Owner, r.RunID)
	if _, err := r.post(ctx, n, protocol.StageComment{
		Stage: protocol.StageApprove,
		Body:  "План підтверджено. У v0 пайплайн зупиняється тут; етап implement з'явиться у v1.",
		Ack:   ack,
	}); err != nil {
		return err
	}
	_, err = r.transition(ctx, n, protocol.TransitionApprove, protocol.StageApprove)
	return err
}

// jobResult is one entry of the `needs` context as toJSON renders it.
type jobResult struct {
	Result string `json:"result"`
}

// ReportFailure is the last job of a run. It reads the `needs` context and,
// when a stage job failed or was cancelled, makes sure the Issue says so:
// the error comment when the stage did not post one itself, and the
// relay:failed labels when the stage posted the comment but died before
// moving them.
func (r *Runner) ReportFailure(ctx context.Context, n int, needsJSON []byte) error {
	var needs map[string]jobResult
	if err := json.Unmarshal(needsJSON, &needs); err != nil {
		return fmt.Errorf("RELAY_NEEDS: %w", err)
	}
	var failed protocol.Stage
	var result string
	for _, s := range []protocol.Stage{protocol.StageDiscuss, protocol.StageTicket, protocol.StageTriage, protocol.StagePlan, protocol.StageApprove} {
		if j, ok := needs[string(s)]; ok && (j.Result == "failure" || j.Result == "cancelled") {
			failed, result = s, j.Result
			break
		}
	}
	if failed == "" {
		r.logf("report-failure: no stage job failed (%s)", string(needsJSON))
		return nil
	}
	issue, labels, err := r.fetch(ctx, n)
	if err != nil {
		return err
	}
	if e, ok := protocol.LastError(issue.Protocol()); ok && r.RunID != "" && e.Run == r.RunID {
		if _, perr := protocol.Apply(labels, protocol.TransitionFailed, e.Stage); perr == nil && labels.State != protocol.StateFailed && e.Stage != protocol.StageDiscuss && e.Stage != protocol.StageApprove {
			r.logf("report-failure: %s reported by this run but labels were not moved; repairing", e.Stage)
			_, err := r.transition(ctx, n, protocol.TransitionFailed, e.Stage)
			return err
		}
		r.logf("report-failure: %s already reported by this run", e.Stage)
		return nil
	}
	return r.fail(ctx, n, failed, fmt.Sprintf("job %s %s (run %s)", failed, result, r.RunID), nil)
}

// commitSpec puts the spec file on the task branch and pushes it. The file
// is moved aside while switching, so that a tracked copy on the branch (a
// re-plan) does not collide with the untracked one Claude just wrote. An
// unchanged spec is not an error: there is nothing to push.
func (r *Runner) commitSpec(ctx context.Context, branch, spec, message string) error {
	git := r.git
	aside := filepath.Join(r.Dir, ContextDir, "spec.md")
	full := filepath.Join(r.Dir, spec)
	if err := os.Rename(full, aside); err != nil {
		return err
	}
	restore := func() error {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.Rename(aside, full)
	}
	created := false
	out, err := git(ctx, "fetch", "origin", branch)
	switch {
	case err == nil:
		if out, err := git(ctx, "switch", "-C", branch, "origin/"+branch); err != nil {
			_ = restore()
			return fmt.Errorf("git switch: %w: %s", err, lastLine(out))
		}
	case strings.Contains(out, "couldn't find remote ref") || strings.Contains(out, "Could not find remote ref"):
		created = true
		if out, err := git(ctx, "switch", "-C", branch); err != nil {
			_ = restore()
			return fmt.Errorf("git switch: %w: %s", err, lastLine(out))
		}
	default:
		_ = restore()
		return fmt.Errorf("git fetch: %w: %s", err, lastLine(out))
	}
	if err := restore(); err != nil {
		return err
	}
	if out, err := git(ctx, "add", spec); err != nil {
		return fmt.Errorf("git add: %w: %s", err, lastLine(out))
	}
	var steps [][]string
	if _, err := git(ctx, "diff", "--cached", "--quiet"); err == nil {
		if !created {
			r.logf("spec %s is unchanged on %s; nothing to push", spec, branch)
			return nil
		}
		// A new branch with an unchanged spec (the spec already exists
		// on the base branch) still has to exist on the remote, since
		// the plan comment links to it.
		r.logf("spec %s is unchanged; pushing the new branch %s anyway", spec, branch)
	} else {
		steps = append(steps, []string{"-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com", "commit", "-m", message})
	}
	steps = append(steps, []string{"push", "-u", "origin", branch})
	for _, s := range steps {
		if out, err := git(ctx, s...); err != nil {
			return fmt.Errorf("git %s: %w: %s", s[len(s)-2], err, lastLine(out))
		}
	}
	r.logf("spec %s pushed to %s", spec, branch)
	return nil
}

func (r *Runner) git(ctx context.Context, args ...string) (string, error) {
	if r.Git != nil {
		return r.Git(ctx, r.Dir, args...)
	}
	return runGit(ctx, r.Dir, args...)
}
