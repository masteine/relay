package stage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/masteine/relay/internal/protocol"
)

// Claude runs `claude -p` for a stage. Exec is injectable so that tests can
// substitute a script that returns a recorded result JSON.
type Claude struct {
	// Bin is the claude executable; "claude" by default.
	Bin string
	// Dir is the working directory for the run: the repository checkout.
	Dir string
	// PluginDir, when set, is passed as --plugin-dir (development only; in
	// production the plugin is installed on the runner).
	PluginDir string
	// Exec runs a command and returns its stdout, stderr and exit code. A
	// deadline is enforced through ctx. nil means the real exec.
	Exec func(ctx context.Context, dir string, name string, args ...string) (stdout, stderr []byte, exit int, err error)
}

// Invocation is one headless run.
type Invocation struct {
	Stage     protocol.Stage
	Prompt    string
	Model     string
	Effort    string
	MaxTurns  int
	Allowed   []string
	Schema    string // JSON Schema for --json-schema, "" for none
	SessionID string // --session-id for the first turn of a discussion
	Resume    string // --resume for later turns
	Deadline  time.Duration
}

// ModelUsage is one entry of the result's modelUsage map.
type ModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// Result is the parsed result JSON of a run. The field names follow the CLI
// (spike S3): success is IsError == false, never Subtype, because an auth
// failure arrives as subtype "success" with is_error true.
type Result struct {
	IsError          bool                  `json:"is_error"`
	Subtype          string                `json:"subtype"`
	TerminalReason   string                `json:"terminal_reason"`
	Text             string                `json:"result"`
	NumTurns         int                   `json:"num_turns"`
	DurationMs       int64                 `json:"duration_ms"`
	TotalCostUSD     float64               `json:"total_cost_usd"`
	SessionID        string                `json:"session_id"`
	StructuredOutput json.RawMessage       `json:"structured_output"`
	ModelUsage       map[string]ModelUsage `json:"modelUsage"`
	Denials          []json.RawMessage     `json:"permission_denials"`

	// Set by Run, not by the CLI.
	ExitCode int    `json:"-"`
	Stderr   string `json:"-"`
	TimedOut bool   `json:"-"`
}

// Usage folds the result into the protocol's cost marker: tokens summed over
// every model, including subagents (spike S3: the top-level usage counts only
// the main agent). Model is the one that produced the most output; haiku
// side calls do not rename an opus stage.
func (r Result) Usage(stage protocol.Stage) protocol.Usage {
	u := protocol.Usage{V: protocol.Version, Stage: stage, Turns: r.NumTurns, Ms: r.DurationMs, USD: r.TotalCostUSD}
	var top string
	var topOut int64 = -1
	for name, m := range r.ModelUsage {
		u.In += m.InputTokens
		u.Out += m.OutputTokens
		u.CacheRead += m.CacheReadInputTokens
		u.CacheCreate += m.CacheCreationInputTokens
		if m.OutputTokens > topOut || (m.OutputTokens == topOut && name < top) {
			top, topOut = name, m.OutputTokens
		}
	}
	u.Model = shortModel(top)
	return u
}

// shortModel turns "claude-sonnet-5" into "sonnet".
func shortModel(name string) string {
	for _, alias := range []string{"fable", "opus", "sonnet", "haiku"} {
		if strings.Contains(name, alias) {
			return alias
		}
	}
	return name
}

// Err explains a failed run for a comment or a log line.
func (r Result) Err() error {
	switch {
	case r.TimedOut:
		return errors.New("deadline exceeded")
	case r.IsError && r.Text != "":
		return fmt.Errorf("claude: %s", strings.TrimSpace(r.Text))
	case r.IsError:
		return fmt.Errorf("claude: %s (%s)", r.Subtype, r.TerminalReason)
	case r.ExitCode != 0:
		return fmt.Errorf("claude exited with %d: %s", r.ExitCode, lastLine(r.Stderr))
	}
	return nil
}

// Preflight refuses to run when the subscription would be bypassed or the
// login is missing. It is cheap and runs before every stage. When a
// CLAUDE_CODE_OAUTH_TOKEN is present (the S1 fallback), it is masked in the
// job log.
func (c *Claude) Preflight(ctx context.Context) error {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return errors.New("ANTHROPIC_API_KEY is set: it would override the subscription login and bill the API; unset it in the runner's environment")
	}
	if tok := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); tok != "" {
		fmt.Printf("::add-mask::%s\n", tok)
	}
	out, stderr, exit, err := c.exec(ctx, "auth", "status")
	if err != nil {
		return fmt.Errorf("claude auth status: %w", err)
	}
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if jerr := json.Unmarshal(out, &st); jerr != nil || exit != 0 {
		return fmt.Errorf("claude auth status failed (exit %d): %s", exit, lastLine(string(stderr)+string(out)))
	}
	if !st.LoggedIn {
		return errors.New("claude is not logged in on the runner: run `claude auth login` as the runner's user (if the runner is a launchd service, check that its plist has no SessionCreate key)")
	}
	if st.AuthMethod == "apiKey" {
		return fmt.Errorf("claude is authenticated with an API key (%s), not a subscription", st.AuthMethod)
	}
	return nil
}

// Args builds the command line for an invocation.
func (c *Claude) Args(inv Invocation) []string {
	args := []string{"-p", inv.Prompt, "--output-format", "json", "--permission-mode", "dontAsk"}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Effort != "" {
		args = append(args, "--effort", inv.Effort)
	}
	if inv.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(inv.MaxTurns))
	}
	if len(inv.Allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(inv.Allowed, ","))
	}
	if inv.Schema != "" {
		args = append(args, "--json-schema", inv.Schema)
	}
	if inv.SessionID != "" {
		args = append(args, "--session-id", inv.SessionID)
	}
	if inv.Resume != "" {
		args = append(args, "--resume", inv.Resume)
	}
	if c.PluginDir != "" {
		args = append(args, "--plugin-dir", c.PluginDir)
	}
	return args
}

// Run executes the invocation and parses the result. A non-nil error means
// the run could not be executed or its output could not be read; a run that
// Claude itself reports as failed returns a Result with IsError set and a
// nil error, so that the caller can still publish the usage.
func (c *Claude) Run(ctx context.Context, inv Invocation) (Result, error) {
	if inv.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, inv.Deadline)
		defer cancel()
	}
	start := time.Now()
	stdout, stderr, exit, err := c.exec(ctx, c.Args(inv)...)
	res := Result{ExitCode: exit, Stderr: string(stderr)}
	if ctx.Err() == context.DeadlineExceeded && len(bytes.TrimSpace(stdout)) == 0 {
		res.TimedOut = true
		res.IsError = true
		res.DurationMs = time.Since(start).Milliseconds()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("run claude: %w", err)
	}
	if len(bytes.TrimSpace(stdout)) == 0 {
		if exit != 0 && len(bytes.TrimSpace(stderr)) > 0 {
			// Argument and session errors are printed to stderr without a
			// result JSON ("Error: Session ID … is already in use.", "No
			// conversation found with session ID: …"), followed by noise
			// from failing hooks; the first line is the message. Report it
			// like a Claude-side failure, so that the stage can retry a
			// session problem and publish the rest.
			res.IsError = true
			res.Text = firstLine(string(stderr))
			res.DurationMs = time.Since(start).Milliseconds()
			return res, nil
		}
		return res, fmt.Errorf("claude produced no output (exit %d): %s", exit, lastLine(string(stderr)))
	}
	if jerr := json.Unmarshal(stdout, &res); jerr != nil {
		return res, fmt.Errorf("claude output is not the result JSON: %w: %s", jerr, firstLine(string(stdout)))
	}
	res.ExitCode = exit
	res.Stderr = string(stderr)
	if exit != 0 {
		res.IsError = true
	}
	return res, nil
}

// secretEnv lists variables the claude child must not inherit: the job's
// GitHub token has write access and would otherwise reach every hook and MCP
// server on the runner.
var secretEnv = map[string]bool{"GITHUB_TOKEN": true, "GH_TOKEN": true, "ACTIONS_RUNTIME_TOKEN": true, "ACTIONS_ID_TOKEN_REQUEST_TOKEN": true}

func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !secretEnv[k] {
			env = append(env, kv)
		}
	}
	return env
}

func (c *Claude) exec(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	if c.Exec != nil {
		return c.Exec(ctx, c.Dir, bin, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.Dir
	cmd.Env = childEnv()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	// claude spawns tools and subagents; on the deadline the whole process
	// group goes, not just the parent, and Wait gives up on a stuck pipe.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
		err = nil
	}
	return out.Bytes(), errb.Bytes(), exit, err
}

// AllowedTools is the tool allowlist per stage (spike S3: the subagent tool
// is Agent; Edit(...) path rules cover every file-editing tool). No
// `git log`: its --output writes any file, and issue text can ask for it.
func AllowedTools(stage protocol.Stage) []string {
	read := []string{"Read", "Grep", "Glob"}
	switch stage {
	case protocol.StageDiscuss, protocol.StageTicket:
		return read
	case protocol.StageTriage:
		return append(read, "Bash(git ls-files:*)")
	case protocol.StagePlan:
		return append(read, "Bash(git ls-files:*)", "Agent", "Edit(docs/specs/**)")
	}
	return read
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// shorten bounds a reason for a comment and a marker.
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}
