package stage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/masteine/relay/internal/protocol"
)

// fakeExec answers `claude auth status` and `claude -p` from canned output.
type fakeExec struct {
	auth   string
	result string
	exit   int
	stderr string
	calls  [][]string
	hang   bool
}

func (f *fakeExec) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		return []byte(f.auth), nil, 0, nil
	}
	if f.hang {
		<-ctx.Done()
		return nil, []byte("killed"), 137, nil
	}
	return []byte(f.result), []byte(f.stderr), f.exit, nil
}

const okResult = `{"type":"result","subtype":"success","is_error":false,"num_turns":3,"duration_ms":35170,"total_cost_usd":0.2305937,
"session_id":"f9fc7470-ab94-464e-bbfe-8f67310bfdda","result":"{\"lines\":4}","structured_output":{"lines":4},
"usage":{"input_tokens":4,"output_tokens":278},
"modelUsage":{"claude-sonnet-5":{"inputTokens":24,"outputTokens":431,"cacheReadInputTokens":92821,"cacheCreationInputTokens":53995,"costUSD":0.2305937}},
"permission_denials":[]}`

const notLoggedIn = `{"type":"result","subtype":"success","is_error":true,"num_turns":1,"duration_ms":161,"total_cost_usd":0,
"session_id":"x","result":"Not logged in · Please run /login","terminal_reason":"api_error","modelUsage":{}}`

func TestPreflight(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	fe := &fakeExec{auth: `{"loggedIn":true,"authMethod":"claude.ai"}`}
	c := &Claude{Exec: fe.run}
	if err := c.Preflight(context.Background()); err != nil {
		t.Errorf("logged in: %v", err)
	}
	fe.auth = `{"loggedIn":false,"authMethod":"none"}`
	if err := c.Preflight(context.Background()); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("not logged in: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	if err := c.Preflight(context.Background()); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("api key set: %v", err)
	}
}

func TestArgs(t *testing.T) {
	c := &Claude{PluginDir: "./plugin"}
	got := c.Args(Invocation{Stage: protocol.StagePlan, Prompt: "/relay:plan .relay/issue.md", Model: "opus", Effort: "high", MaxTurns: 200,
		Allowed: AllowedTools(protocol.StagePlan), Schema: `{"type":"object"}`, Resume: "sid"})
	want := []string{"-p", "/relay:plan .relay/issue.md", "--output-format", "json", "--permission-mode", "dontAsk",
		"--model", "opus", "--effort", "high", "--max-turns", "200",
		"--allowedTools", "Read,Grep,Glob,Bash(git ls-files:*),Agent,Edit(docs/specs/**)",
		"--json-schema", `{"type":"object"}`, "--resume", "sid", "--plugin-dir", "./plugin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if a := AllowedTools(protocol.StageDiscuss); len(a) != 3 || a[0] != "Read" {
		t.Errorf("discuss tools = %v", a)
	}
}

func TestRunParsesResult(t *testing.T) {
	fe := &fakeExec{result: okResult}
	c := &Claude{Exec: fe.run, Dir: "/tmp/x"}
	res, err := c.Run(context.Background(), Invocation{Stage: protocol.StageTriage, Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.NumTurns != 3 || res.SessionID == "" || res.ExitCode != 0 {
		t.Errorf("got %+v", res)
	}
	var so struct{ Lines int }
	if err := json.Unmarshal(res.StructuredOutput, &so); err != nil || so.Lines != 4 {
		t.Errorf("structured_output = %s (%v)", res.StructuredOutput, err)
	}
	u := res.Usage(protocol.StageTriage)
	want := protocol.Usage{V: 1, Stage: protocol.StageTriage, Turns: 3, In: 24, Out: 431, CacheRead: 92821, CacheCreate: 53995, Ms: 35170, USD: 0.2305937, Model: "sonnet"}
	if u != want {
		t.Errorf("Usage\n got %+v\nwant %+v", u, want)
	}
	if res.Err() != nil {
		t.Errorf("Err = %v", res.Err())
	}
}

func TestRunReportsClaudeErrorWithoutGoError(t *testing.T) {
	fe := &fakeExec{result: notLoggedIn, exit: 1}
	c := &Claude{Exec: fe.run}
	res, err := c.Run(context.Background(), Invocation{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Err() == nil || !strings.Contains(res.Err().Error(), "Not logged in") {
		t.Errorf("got %+v err=%v", res, res.Err())
	}
	// Subtype is "success" here, which is why IsError is what counts.
	if res.Subtype != "success" {
		t.Errorf("fixture lost its point: subtype=%q", res.Subtype)
	}
}

func TestRunFailures(t *testing.T) {
	// stderr + non-zero exit without a result JSON is how the CLI reports
	// argument and session errors; the first line is the message.
	c := &Claude{Exec: (&fakeExec{result: "", exit: 1, stderr: "Error: Session ID x is already in use.\nSessionEnd hook [node …] failed: node: command not found"}).run}
	if res, err := c.Run(context.Background(), Invocation{Prompt: "p"}); err != nil || !res.IsError || res.Text != "Error: Session ID x is already in use." || res.ExitCode != 1 {
		t.Errorf("stderr error: res=%+v err=%v", res, err)
	}
	// Empty stdout and empty stderr with a non-zero exit is a wrapper error.
	c = &Claude{Exec: (&fakeExec{result: "", exit: 2}).run}
	if _, err := c.Run(context.Background(), Invocation{Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("empty output: %v", err)
	}
	c = &Claude{Exec: (&fakeExec{result: "<html>"}).run}
	if _, err := c.Run(context.Background(), Invocation{Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "not the result JSON") {
		t.Errorf("garbage output: %v", err)
	}
	c = &Claude{Exec: func(context.Context, string, string, ...string) ([]byte, []byte, int, error) {
		return nil, nil, 0, errors.New("exec: not found")
	}}
	if _, err := c.Run(context.Background(), Invocation{Prompt: "p"}); err == nil {
		t.Error("exec error must surface")
	}
}

func TestRunDeadline(t *testing.T) {
	c := &Claude{Exec: (&fakeExec{hang: true}).run}
	start := time.Now()
	res, err := c.Run(context.Background(), Invocation{Prompt: "p", Deadline: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || !res.IsError || res.Err() == nil || !strings.Contains(res.Err().Error(), "deadline") {
		t.Errorf("got %+v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("deadline was not enforced")
	}
}

func TestUsageSumsModels(t *testing.T) {
	r := Result{NumTurns: 2, ModelUsage: map[string]ModelUsage{
		"claude-opus-5-5":  {InputTokens: 10, OutputTokens: 20, CostUSD: 1},
		"claude-sonnet-5":  {InputTokens: 1, OutputTokens: 2, CacheReadInputTokens: 300},
		"claude-fable-5-1": {InputTokens: 100},
	}, TotalCostUSD: 1.5}
	u := r.Usage(protocol.StagePlan)
	// Tokens are summed; the model is the one with the most output (opus),
	// not the join, so haiku side calls do not rename an opus stage.
	if u.In != 111 || u.Out != 22 || u.CacheRead != 300 || u.Model != "opus" || u.USD != 1.5 {
		t.Errorf("got %+v", u)
	}
}
