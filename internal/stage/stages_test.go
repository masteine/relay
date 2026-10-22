package stage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/masteine/relay/internal/config"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

const testOwner = "masteine"

// stageExec is a fake claude: it returns one canned result per run and can
// run a hook (for example to write the spec file the way the plan skill
// does).
type stageExec struct {
	results []string
	calls   [][]string
	onRun   func(dir string, args []string)
	hang    bool
}

func (s *stageExec) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, int, error) {
	s.calls = append(s.calls, args)
	if s.hang {
		<-ctx.Done()
		return nil, nil, 137, nil
	}
	if s.onRun != nil {
		s.onRun(dir, args)
	}
	if len(s.results) == 0 {
		return nil, []byte("no canned result"), 1, nil
	}
	out := s.results[0]
	s.results = s.results[1:]
	// A canned result starting with "STDERR:" plays the CLI's argument and
	// session errors: nothing on stdout, the message on stderr, exit 1.
	if rest, ok := strings.CutPrefix(out, "STDERR:"); ok {
		return nil, []byte(rest), 1, nil
	}
	return []byte(out), nil, 0, nil
}

// Real CLI output recorded on 2026-10-21 (claude 2.1.281), hook noise included.
const (
	sessionTakenStderr = "STDERR:Error: Session ID d01adc95-a69e-45e1-8b92-835155c4503f is already in use.\n"
	noConversationErr  = "STDERR:No conversation found with session ID: old-session\nSessionEnd hook [node \"$CLAUDE_PLUGIN_ROOT\"/scripts/run.cjs] failed: /bin/sh: node: command not found\n"
)

// resultJSON renders a successful result with the given structured output.
func resultJSON(so any) string {
	raw, _ := json.Marshal(so)
	return fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"num_turns":5,"duration_ms":12000,"total_cost_usd":0.31,
"session_id":"11111111-2222-3333-4444-555555555555","result":%q,"structured_output":%s,
"modelUsage":{"claude-sonnet-5":{"inputTokens":10,"outputTokens":200,"cacheReadInputTokens":1000,"cacheCreationInputTokens":2000,"costUSD":0.31}}}`, string(raw), raw)
}

const errResult = `{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":2,"duration_ms":3000,"total_cost_usd":0.05,"result":"boom","modelUsage":{"claude-sonnet-5":{"inputTokens":1,"outputTokens":2,"costUSD":0.05}}}`

func relayBody(t *testing.T, stage protocol.Stage, upto int64, extra ...any) string {
	t.Helper()
	c := protocol.StageComment{Stage: stage, Body: "…", Ack: protocol.Ack{Upto: upto}}
	for _, e := range extra {
		switch v := e.(type) {
		case *protocol.Session:
			c.Session = v
		case *protocol.ErrorMarker:
			c.Error = v
		}
	}
	s, err := protocol.FormatStageComment(c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type harness struct {
	r    *Runner
	f    *gh.Fake
	ex   *stageExec
	git  [][]string
	logs bytes.Buffer
}

func newHarness(t *testing.T, issue gh.Issue, results ...string) *harness {
	t.Helper()
	h := &harness{f: gh.NewFake(issue), ex: &stageExec{results: results}}
	cfg := config.Defaults()
	cfg.Owner, cfg.Platform, cfg.Preset = testOwner, "android", "android-release"
	h.r = &Runner{
		Repo:   gh.Repo{Owner: "masteine", Name: "relay-sandbox"},
		GH:     h.f,
		Claude: &Claude{Exec: h.ex.run},
		Config: cfg,
		Dir:    t.TempDir(),
		RunID:  "run-1",
		Log:    &h.logs,
		Git: func(ctx context.Context, dir string, args ...string) (string, error) {
			h.git = append(h.git, args)
			switch args[0] {
			case "fetch":
				return "fatal: couldn't find remote ref", errors.New("exit 128")
			case "diff":
				// Exit 1 from `diff --cached --quiet` means there is something to commit.
				return "", errors.New("exit 1")
			}
			return "", nil
		},
	}
	h.r.Claude.Dir = h.r.Dir
	return h
}

func (h *harness) labels(t *testing.T, n int) []string {
	t.Helper()
	is, err := h.f.Issue(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	l, err := protocol.ParseLabels(is.Labels)
	if err != nil {
		t.Fatal(err)
	}
	return l.Strings()
}

func (h *harness) last(t *testing.T, n int) protocol.StageComment {
	t.Helper()
	c, err := protocol.ParseStageComment(h.f.LastComment(n))
	if err != nil {
		t.Fatalf("last comment is not a Relay comment: %v\n%s", err, h.f.LastComment(n))
	}
	return c
}

func hasArg(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func discussIssue() gh.Issue {
	return gh.Issue{Number: 1, Title: "Додати темну тему", Body: "draft", Labels: []string{"relay:discuss"}, Comments: []gh.Comment{
		{ID: 100, Author: testOwner, Body: "todo: dark theme"},
	}}
}

func TestRouteWritesOutputsAndPostsIgnored(t *testing.T) {
	is := discussIssue()
	is.Comments = append(is.Comments,
		gh.Comment{ID: 101, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageDiscuss, 100)},
		gh.Comment{ID: 102, Author: testOwner, Body: "/approve"})
	h := newHarness(t, is)
	payload := []byte(`{"action":"created","issue":{"number":1,"labels":[{"name":"relay:discuss"}]},"comment":{"id":102,"body":"/approve","user":{"login":"masteine"}},"sender":{"login":"masteine"},"repository":{"full_name":"masteine/relay-sandbox","private":true}}`)
	var out bytes.Buffer
	d, err := h.r.Route(context.Background(), "issue_comment", payload, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Ignored() || out.String() != "stages=[\"ignored\"]\nissue=1\n" {
		t.Errorf("decision %+v outputs %q", d, out.String())
	}
	c := h.last(t, 1)
	if c.Stage != protocol.StageIgnored || !strings.Contains(c.Body, "`/approve` проігноровано") || c.Ack.Upto != 102 || c.Ack.Run != "run-1" {
		t.Errorf("ignored comment = %+v", c)
	}
}

func TestRouteTicketChainAndPayloadFallback(t *testing.T) {
	// The triggering comment is in the payload but not yet in the fetched
	// list: route must still see it.
	h := newHarness(t, discussIssue())
	payload := []byte(`{"action":"created","issue":{"number":1,"labels":[{"name":"relay:discuss"}]},"comment":{"id":102,"body":"/ticket","user":{"login":"masteine"}},"sender":{"login":"masteine"},"repository":{"full_name":"masteine/relay-sandbox","private":true}}`)
	var out bytes.Buffer
	d, err := h.r.Route(context.Background(), "issue_comment", payload, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Stages, []protocol.Stage{protocol.StageTicket, protocol.StageTriage, protocol.StagePlan}) {
		t.Errorf("Stages = %v (%s)", d.Stages, d.Reason)
	}
	if out.String() != "stages=[\"ticket\",\"triage\",\"plan\"]\nissue=1\n" || len(h.f.Log) != 0 {
		t.Errorf("outputs %q log %v", out.String(), h.f.Log)
	}
}

func TestRoutePublicRepoDoesNothing(t *testing.T) {
	h := newHarness(t, discussIssue())
	h.f.Private = false
	payload := []byte(`{"action":"created","issue":{"number":1},"comment":{"id":102,"body":"/ticket","user":{"login":"masteine"}},"sender":{"login":"masteine"},"repository":{"full_name":"masteine/relay-sandbox","private":true}}`)
	var out bytes.Buffer
	d, err := h.r.Route(context.Background(), "issue_comment", payload, &out)
	if err != nil || len(d.Stages) != 0 || out.String() != "stages=[]\nissue=1\n" {
		t.Errorf("d=%+v err=%v out=%q", d, err, out.String())
	}
}

func TestDiscussFirstTurnThenResume(t *testing.T) {
	h := newHarness(t, discussIssue(), resultJSON(discussOut{Body: "Зрозумів. Два уточнення…", ReadyForTicket: false}))
	if err := h.r.Discuss(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	args := h.ex.calls[0]
	sid := argValue(args, "--session-id")
	if sid == "" || argValue(args, "--resume") != "" || !hasArg(args, "--model", "sonnet") || !hasArg(args, "--effort", "medium") || !hasArg(args, "--max-turns", "200") {
		t.Errorf("first turn args = %q", args)
	}
	if !strings.HasPrefix(args[1], "/relay:discuss .relay/issue.md") {
		t.Errorf("prompt = %q", args[1])
	}
	if _, err := os.Stat(filepath.Join(h.r.Dir, ".relay", "issue.md")); err != nil {
		t.Error("context file not written")
	}
	c := h.last(t, 1)
	if c.Stage != protocol.StageDiscuss || c.Body != "Зрозумів. Два уточнення…" || c.Ack.Upto != 100 || c.Session == nil || c.Usage == nil || c.Usage.In != 10 {
		t.Errorf("discuss comment = %+v", c)
	}
	if h.labels(t, 1)[0] != "relay:discuss" {
		t.Errorf("labels changed: %v", h.labels(t, 1))
	}

	// Second turn: the owner replies; the session is resumed.
	h.f.Issues[1].Comments = append(h.f.Issues[1].Comments, gh.Comment{ID: 2000, Author: testOwner, Body: "yes"})
	h.ex.results = []string{resultJSON(discussOut{Body: "Готово, можна /ticket.", ReadyForTicket: true})}
	if err := h.r.Discuss(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	args = h.ex.calls[1]
	if argValue(args, "--resume") != c.Session.ID || argValue(args, "--session-id") != "" {
		t.Errorf("second turn args = %q", args)
	}
	if c2 := h.last(t, 1); c2.Ack.Upto != 2000 {
		t.Errorf("second ack = %d, want 2000", c2.Ack.Upto)
	}
}

func TestDiscussFailurePostsErrorAndKeepsLabels(t *testing.T) {
	// A first turn that fails for a reason other than a taken session is
	// reported as is, without a retry.
	h := newHarness(t, discussIssue(), errResult)
	err := h.r.Discuss(context.Background(), 1, time.Minute)
	if !errors.Is(err, ErrStage) {
		t.Fatalf("err = %v, want ErrStage", err)
	}
	if len(h.ex.calls) != 1 {
		t.Errorf("expected a single call, got %q", h.ex.calls)
	}
	c := h.last(t, 1)
	if c.Stage != protocol.StageError || c.Error == nil || c.Error.Stage != protocol.StageDiscuss || c.Error.Run != "run-1" || !strings.Contains(c.Body, "boom") {
		t.Errorf("error comment = %+v", c)
	}
	if c.Ack.Upto != 0 {
		t.Errorf("error comment must not consume input, ack = %d", c.Ack.Upto)
	}
	if got := h.labels(t, 1); !reflect.DeepEqual(got, []string{"relay:discuss"}) {
		t.Errorf("labels = %v, want unchanged", got)
	}
}

func TestTicket(t *testing.T) {
	is := discussIssue()
	is.Comments = append(is.Comments,
		gh.Comment{ID: 101, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageDiscuss, 100)},
		gh.Comment{ID: 102, Author: testOwner, Body: "/ticket"})
	h := newHarness(t, is, resultJSON(ticketOut{Title: "Додати темну тему з перемикачем", Body: "## Задача\nТемна тема."}))
	if err := h.r.Ticket(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, _ := h.f.Issue(context.Background(), 1)
	if got.Title != "Додати темну тему з перемикачем" || got.Body != "## Задача\nТемна тема." {
		t.Errorf("issue not rewritten: %+v", got)
	}
	if l := h.labels(t, 1); !reflect.DeepEqual(l, []string{"relay", "relay:triage"}) {
		t.Errorf("labels = %v", l)
	}
	c := h.last(t, 1)
	if c.Stage != protocol.StageTicket || c.Ack.Upto != 102 || c.Usage == nil {
		t.Errorf("ticket comment = %+v", c)
	}
}

func TestTriageManualStart(t *testing.T) {
	is := gh.Issue{Number: 8, Title: "Manual", Body: "by hand", Labels: []string{"bug", "relay"}}
	h := newHarness(t, is, resultJSON(triageOut{Body: "**Обсяг:** S", Size: "S", KeyFiles: []string{"go.mod"}}))
	if err := h.r.Triage(context.Background(), 8, time.Minute); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 8); !reflect.DeepEqual(l, []string{"bug", "relay", "relay:plan"}) {
		t.Errorf("labels = %v", l)
	}
	c := h.last(t, 8)
	if c.Stage != protocol.StageTriage || c.Body != "**Обсяг:** S" || c.Ack.Upto != 0 {
		t.Errorf("triage comment = %+v", c)
	}
	if !hasArg(h.ex.calls[0], "--model", "opus") || !hasArg(h.ex.calls[0], "--effort", "high") {
		t.Errorf("triage must run on the thinking model: %q", h.ex.calls[0])
	}
	if tools := argValue(h.ex.calls[0], "--allowedTools"); !strings.Contains(tools, "Bash(git ls-files:*)") || strings.Contains(tools, "git log") {
		t.Errorf("triage tools: %q", argValue(h.ex.calls[0], "--allowedTools"))
	}
}

func TestTriageWrongStateIsAnError(t *testing.T) {
	h := newHarness(t, discussIssue())
	if err := h.r.Triage(context.Background(), 1, time.Minute); err == nil || len(h.ex.calls) != 0 {
		t.Errorf("err=%v calls=%d", err, len(h.ex.calls))
	}
}

func planIssue() gh.Issue {
	return gh.Issue{Number: 42, Title: "Додати команду /status у бота", Body: "## Задача\n…", Labels: []string{"relay", "relay:plan"}, Comments: []gh.Comment{
		{ID: 103, Author: testOwner, Body: "/ticket"},
		{ID: 104, Author: protocol.BotLogin, Body: "## Relay · ticket\n\n…\n\n<!-- relay-ack {\"v\":1,\"upto\":103} -->\n"},
		{ID: 106, Author: testOwner, Body: "typed while the chain runs"},
	}}
}

func TestPlanCommitsSpecAndAsksApproval(t *testing.T) {
	spec := "docs/specs/42-dodaty-komandu-status-u-bota.md"
	h := newHarness(t, planIssue(), resultJSON(planOut{Body: "**ADR.** …", SpecPath: spec, Verdict: "APPROVE", Iterations: 2}))
	h.ex.onRun = func(dir string, args []string) {
		_ = os.MkdirAll(filepath.Join(dir, "docs", "specs"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, spec), []byte("# План\n"), 0o644)
	}
	if err := h.r.Plan(context.Background(), 42, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(argValue(h.ex.calls[0], "--allowedTools"), "Edit(docs/specs/**)") || !strings.Contains(argValue(h.ex.calls[0], "--allowedTools"), "Agent") {
		t.Errorf("plan tools: %q", argValue(h.ex.calls[0], "--allowedTools"))
	}
	var gitCmds []string
	for _, g := range h.git {
		gitCmds = append(gitCmds, strings.Join(g, " "))
	}
	joined := strings.Join(gitCmds, "\n")
	for _, want := range []string{"fetch origin relay/42-dodaty-komandu-status-u-bota", "switch -C relay/42-dodaty-komandu-status-u-bota", "add " + spec, "commit -m Add spec for #42", "push -u origin relay/42-dodaty-komandu-status-u-bota"} {
		if !strings.Contains(joined, want) {
			t.Errorf("git calls lack %q:\n%s", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(h.r.Dir, spec)); err != nil {
		t.Error("spec must be back in place after the switch")
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:plan", "needs-approval"}) {
		t.Errorf("labels = %v", l)
	}
	c := h.last(t, 42)
	if c.Stage != protocol.StagePlan || c.Ack.Upto != 103 || c.Usage == nil {
		t.Errorf("plan comment = %+v (chained stages keep the watermark, so #106 stays pending)", c)
	}
}

func TestPlanMissingSpecFails(t *testing.T) {
	h := newHarness(t, planIssue(), resultJSON(planOut{Body: "…", SpecPath: "docs/specs/42-dodaty-komandu-status-u-bota.md", Verdict: "APPROVE", Iterations: 1}))
	err := h.r.Plan(context.Background(), 42, time.Minute)
	if !errors.Is(err, ErrStage) {
		t.Fatalf("err = %v", err)
	}
	c := h.last(t, 42)
	if c.Stage != protocol.StageError || c.Error.Stage != protocol.StagePlan || !strings.Contains(c.Body, "did not write") {
		t.Errorf("error comment = %+v", c)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:failed"}) {
		t.Errorf("labels = %v", l)
	}
	if len(h.git) != 0 {
		t.Errorf("git must not run: %v", h.git)
	}
}

func TestPlanRejectsWrongSpecPath(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "docs/specs/7-other-task.md", "docs/specs"} {
		h := newHarness(t, planIssue(), resultJSON(planOut{Body: "…", SpecPath: bad, Verdict: "APPROVE", Iterations: 1}))
		err := h.r.Plan(context.Background(), 42, time.Minute)
		if !errors.Is(err, ErrStage) {
			t.Errorf("%s: err = %v, want ErrStage", bad, err)
			continue
		}
		if c := h.last(t, 42); c.Stage != protocol.StageError || !strings.Contains(c.Body, "instead of") {
			t.Errorf("%s: error comment = %+v", bad, c)
		}
		if len(h.git) != 0 {
			t.Errorf("%s: git must not run", bad)
		}
	}
}

// failedAt builds an Issue that failed at stage, with /retry pending.
func failedAt(t *testing.T, stage protocol.Stage) gh.Issue {
	t.Helper()
	return gh.Issue{Number: 42, Title: "Додати команду /status у бота", Body: "## Задача\n…", Labels: []string{"relay", "relay:failed"}, Comments: []gh.Comment{
		{ID: 103, Author: testOwner, Body: "/ticket"},
		{ID: 104, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageTicket, 103)},
		{ID: 108, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageError, 103, &protocol.ErrorMarker{Stage: stage, Reason: "deadline", Run: "run-0"})},
		{ID: 109, Author: testOwner, Body: "/retry"},
	}}
}

func TestRetryTriage(t *testing.T) {
	h := newHarness(t, failedAt(t, protocol.StageTriage), resultJSON(triageOut{Body: "**Обсяг:** S", Size: "S"}))
	if err := h.r.Triage(context.Background(), 42, time.Minute); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:plan"}) {
		t.Errorf("labels = %v", l)
	}
	if c := h.last(t, 42); c.Stage != protocol.StageTriage || c.Ack.Upto != 109 {
		t.Errorf("a retried stage must ack the /retry: %+v", c)
	}
}

func TestRetryTicket(t *testing.T) {
	is := failedAt(t, protocol.StageTicket)
	is.Labels = []string{"relay", "relay:failed"}
	h := newHarness(t, is, resultJSON(ticketOut{Title: "T", Body: "## Задача\nx"}))
	if err := h.r.Ticket(context.Background(), 42, time.Minute); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:triage"}) {
		t.Errorf("labels = %v", l)
	}
	if c := h.last(t, 42); c.Stage != protocol.StageTicket || c.Ack.Upto != 109 {
		t.Errorf("ticket comment = %+v", c)
	}
}

func TestRetryPlan(t *testing.T) {
	spec := "docs/specs/42-dodaty-komandu-status-u-bota.md"
	h := newHarness(t, failedAt(t, protocol.StagePlan), resultJSON(planOut{Body: "…", SpecPath: spec, Verdict: "APPROVE", Iterations: 1}))
	h.ex.onRun = func(dir string, _ []string) {
		_ = os.MkdirAll(filepath.Join(dir, "docs", "specs"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, spec), []byte("# План\n"), 0o644)
	}
	if err := h.r.Plan(context.Background(), 42, time.Minute); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:plan", "needs-approval"}) {
		t.Errorf("labels = %v", l)
	}
	if c := h.last(t, 42); c.Stage != protocol.StagePlan || c.Ack.Upto != 109 {
		t.Errorf("plan comment = %+v", c)
	}
}

func TestRetryOfAnotherStageSkips(t *testing.T) {
	h := newHarness(t, failedAt(t, protocol.StagePlan))
	err := h.r.Triage(context.Background(), 42, time.Minute)
	if !errors.Is(err, ErrSkip) || len(h.ex.calls) != 0 || len(h.f.Log) != 0 {
		t.Errorf("err=%v calls=%d log=%v", err, len(h.ex.calls), h.f.Log)
	}
}

func TestSkipOnWrongState(t *testing.T) {
	waiting := planIssue()
	waiting.Labels = []string{"relay", "relay:plan", "needs-approval"}
	h := newHarness(t, waiting)
	if err := h.r.Plan(context.Background(), 42, time.Minute); !errors.Is(err, ErrSkip) || len(h.f.Log) != 0 {
		t.Errorf("plan while waiting for approval: err=%v log=%v", err, h.f.Log)
	}
	h = newHarness(t, planIssue())
	if err := h.r.Approve(context.Background(), 42); !errors.Is(err, ErrSkip) || len(h.f.Log) != 0 {
		t.Errorf("approve without needs-approval: err=%v log=%v", err, h.f.Log)
	}
	if err := h.r.Triage(context.Background(), 42, time.Minute); !errors.Is(err, ErrSkip) {
		t.Errorf("triage on relay:plan: err=%v", err)
	}
}

func TestDiscussResumeFallsBackToFreshSession(t *testing.T) {
	is := discussIssue()
	is.Comments = append(is.Comments,
		gh.Comment{ID: 101, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageDiscuss, 100, &protocol.Session{ID: "old-session"})},
		gh.Comment{ID: 102, Author: testOwner, Body: "and the icon?"})
	h := newHarness(t, is, noConversationErr, resultJSON(discussOut{Body: "Так, іконку теж."}))
	if err := h.r.Discuss(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(h.ex.calls) != 2 || argValue(h.ex.calls[0], "--resume") != "old-session" {
		t.Fatalf("calls = %q", h.ex.calls)
	}
	fresh := argValue(h.ex.calls[1], "--session-id")
	if fresh == "" || fresh == "old-session" || argValue(h.ex.calls[1], "--resume") != "" {
		t.Errorf("second call must start a fresh session: %q", h.ex.calls[1])
	}
	c := h.last(t, 1)
	if c.Stage != protocol.StageDiscuss || c.Session == nil || c.Session.ID == "old-session" || c.Ack.Upto != 102 {
		t.Errorf("comment = %+v", c)
	}
}

func TestDiscussTakenSessionIsResumed(t *testing.T) {
	h := newHarness(t, discussIssue(), sessionTakenStderr, resultJSON(discussOut{Body: "ok"}))
	if err := h.r.Discuss(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(h.ex.calls) != 2 || argValue(h.ex.calls[1], "--resume") != argValue(h.ex.calls[0], "--session-id") {
		t.Errorf("calls = %q", h.ex.calls)
	}
}

func TestDiscussOtherErrorIsNotRetried(t *testing.T) {
	h := newHarness(t, discussIssue(), `{"type":"result","is_error":true,"result":"Reached max turns (200)","modelUsage":{}}`)
	err := h.r.Discuss(context.Background(), 1, time.Minute)
	if !errors.Is(err, ErrStage) || len(h.ex.calls) != 1 {
		t.Errorf("err=%v calls=%d", err, len(h.ex.calls))
	}
	// The same on a resumed session: max turns is not a session problem,
	// so no fresh session and no second (double-cost) turn.
	is := discussIssue()
	is.Comments = append(is.Comments, gh.Comment{ID: 101, Author: protocol.BotLogin, Body: relayBody(t, protocol.StageDiscuss, 100, &protocol.Session{ID: "old-session"})}, gh.Comment{ID: 102, Author: testOwner, Body: "more"})
	h = newHarness(t, is, `{"type":"result","is_error":true,"result":"Reached max turns (200)","modelUsage":{}}`)
	if err := h.r.Discuss(context.Background(), 1, time.Minute); !errors.Is(err, ErrStage) || len(h.ex.calls) != 1 {
		t.Errorf("resumed: err=%v calls=%d", err, len(h.ex.calls))
	}
	if c := h.last(t, 1); c.Stage != protocol.StageError || !strings.Contains(c.Body, "max turns") {
		t.Errorf("error comment = %+v", c)
	}
}

func TestReportFailureRepairsLabelsAfterPartialFail(t *testing.T) {
	is := planIssue()
	is.Labels = []string{"relay", "relay:triage"}
	h := newHarness(t, is, errResult)
	// The stage posts its error comment, then the label move fails.
	h.f.FailOn["AddLabels"] = errors.New("502")
	err := h.r.Triage(context.Background(), 42, time.Minute)
	if err == nil || errors.Is(err, ErrStage) {
		t.Fatalf("err = %v, want the AddLabels error", err)
	}
	if c := h.last(t, 42); c.Stage != protocol.StageError || c.Error.Run != "run-1" {
		t.Fatalf("error comment = %+v", c)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:triage"}) {
		t.Fatalf("labels = %v", l)
	}
	// report-failure in the same run sees the comment and moves the labels.
	delete(h.f.FailOn, "AddLabels")
	n := h.f.Comments(42)
	if err := h.r.ReportFailure(context.Background(), 42, []byte(`{"triage":{"result":"failure"}}`)); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:failed"}) {
		t.Errorf("labels after repair = %v", l)
	}
	if h.f.Comments(42) != n {
		t.Error("repair must not post a second error comment")
	}
}

func TestReasonIsShortened(t *testing.T) {
	long := strings.Repeat("слово ", 80)
	h := newHarness(t, discussIssue(), fmt.Sprintf(`{"type":"result","is_error":true,"result":%q,"modelUsage":{}}`, long))
	_ = h.r.Discuss(context.Background(), 1, time.Minute)
	c := h.last(t, 1)
	if c.Error == nil || len(c.Error.Reason) > 210 || !strings.HasSuffix(c.Error.Reason, "…") {
		t.Errorf("reason not shortened: %d chars", len(c.Error.Reason))
	}
}

// TestCommitSpecRealGit drives commitSpec against a real repository with a
// bare remote: a new branch, an unchanged re-plan, and a changed re-plan
// from a checkout that is back on the default branch.
func TestCommitSpecRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	bare := t.TempDir()
	work := t.TempDir()
	mustGit := func(dir string, args ...string) string {
		t.Helper()
		out, err := runGit(ctx, dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	mustGit(bare, "init", "--bare", "-b", "main")
	mustGit(work, "init", "-b", "main")
	mustGit(work, "config", "user.email", "t@example.com")
	mustGit(work, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(work, "add", "README.md")
	mustGit(work, "commit", "-m", "init")
	mustGit(work, "remote", "add", "origin", bare)
	mustGit(work, "push", "-u", "origin", "main")

	r := &Runner{Dir: work, Log: io.Discard}
	spec := "docs/specs/1-x.md"
	writeSpec := func(content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(work, "docs", "specs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, spec), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(work, ContextDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. New branch.
	writeSpec("# v1\n")
	if err := r.commitSpec(ctx, "relay/1-x", spec, "spec v1"); err != nil {
		t.Fatalf("new branch: %v", err)
	}
	if out := mustGit(bare, "show", "relay/1-x:"+spec); out != "# v1\n" {
		t.Errorf("remote has %q", out)
	}
	// 2. Unchanged re-plan: nothing to push, no error.
	writeSpec("# v1\n")
	if err := r.commitSpec(ctx, "relay/1-x", spec, "spec v1 again"); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	if n := strings.Count(mustGit(bare, "log", "--oneline", "relay/1-x"), "\n"); n != 2 {
		t.Errorf("unchanged re-plan must not add a commit, log has %d lines", n)
	}
	// 3. Changed re-plan from a checkout back on main (fresh job), with the
	// branch already on the remote: the untracked spec must survive the switch.
	mustGit(work, "switch", "main")
	writeSpec("# v2\n")
	if err := r.commitSpec(ctx, "relay/1-x", spec, "spec v2"); err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if out := mustGit(bare, "show", "relay/1-x:"+spec); out != "# v2\n" {
		t.Errorf("remote has %q after re-plan", out)
	}
	if n := strings.Count(mustGit(bare, "log", "--oneline", "relay/1-x"), "\n"); n != 3 {
		t.Errorf("log has %d lines, want 3", n)
	}
	// The committer identity did not leak into the repository config.
	if out, _ := runGit(ctx, work, "config", "user.name"); strings.TrimSpace(out) != "t" {
		t.Errorf("user.name changed to %q", strings.TrimSpace(out))
	}
}

// TestWorkflowTemplate checks the generated workflow against the binary:
// deadlines fit in timeouts, Claude-running jobs have a checkout, the ones
// without a checkout read relay.yml through the API, and the protocol
// version matches.
func TestWorkflowTemplate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugin", "templates", "workflow.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Env  map[string]string `yaml:"env"`
		Jobs map[string]struct {
			Timeout int `yaml:"timeout-minutes"`
			Steps   []struct {
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	if wf.Env["RELAY_PROTOCOL"] != strconv.Itoa(protocol.Version) {
		t.Errorf("RELAY_PROTOCOL = %q, binary speaks v%d", wf.Env["RELAY_PROTOCOL"], protocol.Version)
	}
	deadlineRe := regexp.MustCompile(`relay stage (\S+).*--deadline (\S+)`)
	for name, job := range wf.Jobs {
		checkout := false
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "actions/checkout") {
				checkout = true
			}
			if m := deadlineRe.FindStringSubmatch(s.Run); m != nil {
				d, err := time.ParseDuration(m[2])
				if err != nil {
					t.Errorf("%s: bad deadline %q", name, m[2])
					continue
				}
				if d+deadlineMargin > time.Duration(job.Timeout)*time.Minute {
					t.Errorf("%s: deadline %s + margin %s exceeds timeout-minutes %d", name, d, deadlineMargin, job.Timeout)
				}
			}
		}
		switch name {
		case "discuss", "ticket", "triage", "plan":
			if !checkout {
				t.Errorf("%s runs Claude in the workspace and needs a checkout", name)
			}
		case "route", "report-failure", "approve":
			if checkout {
				t.Errorf("%s must not touch the shared workspace", name)
			}
		}
	}
}

func TestApprove(t *testing.T) {
	is := planIssue()
	is.Labels = []string{"relay", "relay:plan", "needs-approval"}
	is.Comments = append(is.Comments, gh.Comment{ID: 110, Author: protocol.BotLogin, Body: relayBody(t, protocol.StagePlan, 103)}, gh.Comment{ID: 111, Author: testOwner, Body: "/approve"})
	h := newHarness(t, is)
	if err := h.r.Approve(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:plan"}) {
		t.Errorf("labels = %v", l)
	}
	c := h.last(t, 42)
	if c.Stage != protocol.StageApprove || c.Ack.Upto != 111 || c.Usage != nil {
		t.Errorf("approve comment = %+v", c)
	}
	// Twice: the transition is denied and nothing is posted.
	before := len(h.f.Log)
	if err := h.r.Approve(context.Background(), 42); err == nil || len(h.f.Log) != before {
		t.Errorf("second approve: err=%v log=%v", err, h.f.Log[before:])
	}
}

func TestReportFailureIsIdempotentPerRun(t *testing.T) {
	is := planIssue()
	is.Labels = []string{"relay", "relay:triage"}
	h := newHarness(t, is)
	needs := []byte(`{"route":{"result":"success"},"ticket":{"result":"skipped"},"triage":{"result":"cancelled"},"plan":{"result":"skipped"}}`)
	if err := h.r.ReportFailure(context.Background(), 42, needs); err != nil {
		t.Fatal(err)
	}
	c := h.last(t, 42)
	if c.Stage != protocol.StageError || c.Error.Stage != protocol.StageTriage || c.Error.Run != "run-1" || c.Usage != nil {
		t.Errorf("error comment = %+v", c)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:failed"}) {
		t.Errorf("labels = %v", l)
	}
	n := len(h.f.Log)
	if err := h.r.ReportFailure(context.Background(), 42, needs); err != nil || len(h.f.Log) != n {
		t.Errorf("second report in the same run must be a no-op: err=%v log=%v", err, h.f.Log[n:])
	}
	// Nothing failed → nothing happens.
	if err := h.r.ReportFailure(context.Background(), 42, []byte(`{"triage":{"result":"success"}}`)); err != nil || len(h.f.Log) != n {
		t.Errorf("success run: err=%v log=%v", err, h.f.Log[n:])
	}
	if err := h.r.ReportFailure(context.Background(), 42, []byte(`nope`)); err == nil {
		t.Error("bad RELAY_NEEDS must be an error")
	}
}

func TestDeadlineFailsTheStage(t *testing.T) {
	is := planIssue()
	is.Labels = []string{"relay", "relay:triage"}
	h := newHarness(t, is)
	h.ex.hang = true
	err := h.r.Triage(context.Background(), 42, 30*time.Millisecond)
	if !errors.Is(err, ErrStage) {
		t.Fatalf("err = %v", err)
	}
	c := h.last(t, 42)
	if c.Stage != protocol.StageError || !strings.Contains(c.Body, "deadline") || c.Usage == nil || c.Usage.Ms < 30 || c.Usage.Ms > 5000 {
		t.Errorf("error comment = %+v", c)
	}
	if l := h.labels(t, 42); !reflect.DeepEqual(l, []string{"relay", "relay:failed"}) {
		t.Errorf("labels = %v", l)
	}
}

func TestInvokeWarnsAboutFable(t *testing.T) {
	h := newHarness(t, discussIssue(), resultJSON(discussOut{Body: "ok"}))
	h.r.Config.Models["discuss"] = config.Model{Model: "fable", Effort: "high"}
	if err := h.r.Discuss(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.logs.String(), "fable") || !hasArg(h.ex.calls[0], "--model", "fable") {
		t.Errorf("logs: %s", h.logs.String())
	}
}
