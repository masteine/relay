package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// MinClaude is the oldest Claude Code the stages were verified with
// (spikes S1–S3, M2).
const MinClaude = "2.1.281"

// PluginRef is how the plugin shows up in `claude plugin list`.
const PluginRef = "relay@relay"

// Check is one line of `relay doctor`.
type Check struct {
	Name   string
	OK     bool
	Detail string
	// Fix says what to do when OK is false.
	Fix string
}

func (c Check) String() string {
	mark := "✔"
	if !c.OK {
		mark = "✘"
	}
	s := fmt.Sprintf("%s %s: %s", mark, c.Name, c.Detail)
	if !c.OK && c.Fix != "" {
		s += "\n    → " + c.Fix
	}
	return s
}

// AllOK reports whether every check passed.
func AllOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// versionAtLeast compares dotted versions found inside the strings.
func versionAtLeast(have, min string) bool {
	h, m := versionRe.FindStringSubmatch(have), versionRe.FindStringSubmatch(min)
	if h == nil || m == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.Atoi(h[i])
		b, _ := strconv.Atoi(m[i])
		if a != b {
			return a > b
		}
	}
	return true
}

// CheckClaude: installed, recent enough, logged in with a subscription.
func (s *Sys) CheckClaude(ctx context.Context) Check {
	ver, err := s.run(ctx, "claude", "--version")
	if err != nil {
		return Check{Name: "claude", Detail: err.Error(), Fix: "install Claude Code: https://code.claude.com/docs"}
	}
	ver = strings.TrimSpace(ver)
	if !versionAtLeast(ver, MinClaude) {
		return Check{Name: "claude", Detail: ver, Fix: "update Claude Code to " + MinClaude + " or newer"}
	}
	out, err := s.run(ctx, "claude", "auth", "status")
	if err != nil {
		return Check{Name: "claude", Detail: ver + ", auth status failed: " + err.Error(), Fix: "run `claude auth login`"}
	}
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
		Email      string `json:"email"`
	}
	if jerr := json.Unmarshal([]byte(out), &st); jerr != nil || !st.LoggedIn {
		return Check{Name: "claude", Detail: ver + ", not logged in", Fix: "run `claude auth login` (the browser login of the desktop app does not count for the CLI)"}
	}
	if st.AuthMethod == "apiKey" {
		return Check{Name: "claude", Detail: ver + ", logged in with an API key", Fix: "log in with the subscription: `claude auth login`"}
	}
	return Check{Name: "claude", OK: true, Detail: fmt.Sprintf("%s, logged in via %s (%s)", ver, st.AuthMethod, st.Email)}
}

var scopesRe = regexp.MustCompile(`(?im)^x-oauth-scopes:\s*(.*)$`)

// CheckGH: gh is logged in and the token has repo and workflow scopes. A
// fine-grained token sends no scopes header; that is reported, not failed.
// It returns the login the token belongs to.
func (s *Sys) CheckGH(ctx context.Context) (string, Check) {
	out, err := s.run(ctx, "gh", "api", "user", "-i")
	if err != nil {
		return "", Check{Name: "gh", Detail: err.Error(), Fix: "install gh (`brew install gh`) and run `gh auth login -s repo,workflow`"}
	}
	var login string
	if i := strings.Index(out, "{"); i >= 0 {
		var u struct {
			Login string `json:"login"`
		}
		_ = json.Unmarshal([]byte(out[i:]), &u)
		login = u.Login
	}
	if login == "" {
		return "", Check{Name: "gh", Detail: "could not read the login", Fix: "run `gh auth login -s repo,workflow`"}
	}
	m := scopesRe.FindStringSubmatch(out)
	if m == nil {
		return login, Check{Name: "gh", OK: true, Detail: "logged in as " + login + "; the token sends no scopes header (fine-grained?), so scopes cannot be verified: it needs Issues, Contents and Actions write"}
	}
	scopes := map[string]bool{}
	for _, sc := range strings.Split(m[1], ",") {
		scopes[strings.TrimSpace(sc)] = true
	}
	var missing []string
	for _, need := range []string{"repo", "workflow"} {
		if !scopes[need] {
			missing = append(missing, need)
		}
	}
	if len(missing) > 0 {
		return login, Check{Name: "gh", Detail: fmt.Sprintf("logged in as %s, missing scopes: %s", login, strings.Join(missing, ", ")), Fix: "run `gh auth refresh -s repo,workflow`"}
	}
	return login, Check{Name: "gh", OK: true, Detail: "logged in as " + login + " with repo and workflow"}
}

// CheckAPIKey: ANTHROPIC_API_KEY must be absent from the shell and from
// launchd, or it overrides the subscription on the runner.
func (s *Sys) CheckAPIKey(ctx context.Context) Check {
	if s.Env("ANTHROPIC_API_KEY") != "" {
		return Check{Name: "ANTHROPIC_API_KEY", Detail: "set in this shell", Fix: "unset it: the runner would bill the API instead of the subscription"}
	}
	out, _, _, _ := s.Exec(ctx, "launchctl", "getenv", "ANTHROPIC_API_KEY")
	if strings.TrimSpace(out) != "" {
		return Check{Name: "ANTHROPIC_API_KEY", Detail: "set in launchd", Fix: "run `launchctl unsetenv ANTHROPIC_API_KEY` and remove it from your login items"}
	}
	return Check{Name: "ANTHROPIC_API_KEY", OK: true, Detail: "not set"}
}

var pluginVersionRe = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(PluginRef) + `\s*\n\s*Version:\s*(\S+)`)

// CheckPlugin: the Relay plugin is installed for the user, and its version
// matches this binary's (a release binary with a marketplace HEAD plugin, or
// the other way round, is the skew the plan warns about). A "dev" binary
// does not compare.
func (s *Sys) CheckPlugin(ctx context.Context) Check {
	out, err := s.run(ctx, "claude", "plugin", "list")
	if err != nil {
		return Check{Name: "plugin", Detail: err.Error(), Fix: "run `relay setup`"}
	}
	if !strings.Contains(out, PluginRef) {
		return Check{Name: "plugin", Detail: PluginRef + " is not installed", Fix: "run `relay setup`"}
	}
	ver := ""
	if m := pluginVersionRe.FindStringSubmatch(out); m != nil {
		ver = m[1]
	}
	// Only the numeric parts compare: "0.1.0-dev" and "v0.1.0" are the same release.
	if mine, theirs := versionRe.FindString(s.Version), versionRe.FindString(ver); mine != "" && theirs != "" && mine != theirs {
		return Check{Name: "plugin", Detail: fmt.Sprintf("%s version %s, relay binary %s", PluginRef, ver, s.Version), Fix: "update both: the relay binary (see README) and `claude plugin update relay@relay`"}
	}
	if ver == "" {
		ver = "unknown version"
	}
	return Check{Name: "plugin", OK: true, Detail: fmt.Sprintf("%s installed (%s)", PluginRef, ver)}
}

var (
	stateRe    = regexp.MustCompile(`(?m)^\s*state = ([^\n]+)`)
	lastExitRe = regexp.MustCompile(`(?m)^\s*last exit code = (\S+)`)
)

// agentState asks launchd about a LaunchAgent: "running", "not running",
// "not loaded", with the last exit code when it is not zero.
func (s *Sys) agentState(ctx context.Context, label string) string {
	out, _, exit, err := s.Exec(ctx, "launchctl", "print", "gui/"+s.uid()+"/"+label)
	if err != nil || exit != 0 {
		return "not loaded"
	}
	state := "unknown"
	if m := stateRe.FindStringSubmatch(out); m != nil {
		state = strings.TrimSpace(m[1])
	}
	if m := lastExitRe.FindStringSubmatch(out); m != nil && m[1] != "0" && m[1] != "(never" {
		state += ", last exit " + m[1]
	}
	return state
}

// CheckBotService: the bot's LaunchAgent exists, the config validates, and
// launchd says the bot is running.
func (s *Sys) CheckBotService(ctx context.Context) Check {
	if _, err := os.Stat(s.botPlist()); err != nil {
		return Check{Name: "bot", Detail: "LaunchAgent " + BotLabel + " is not installed", Fix: "run `relay setup`"}
	}
	if _, err := core.LoadConfig(s.configPath()); err != nil {
		return Check{Name: "bot", Detail: err.Error(), Fix: "fix " + s.configPath() + " or run `relay setup` again"}
	}
	state := s.agentState(ctx, BotLabel)
	if state != "running" {
		return Check{Name: "bot", Detail: BotLabel + " is " + state, Fix: "see " + filepath.Join(s.logsDir(), "bot.log") + "; `launchctl kickstart -k gui/" + s.uid() + "/" + BotLabel + "` restarts it"}
	}
	return Check{Name: "bot", OK: true, Detail: BotLabel + " running, config " + s.configPath()}
}

// CheckRunners: every registered runner can find claude and relay on its
// PATH, its plist has no SessionCreate (spike S1), and launchd runs it.
func (s *Sys) CheckRunners(ctx context.Context) []Check {
	entries, err := os.ReadDir(s.runnersDir())
	if err != nil || len(entries) == 0 {
		return []Check{{Name: "runners", Detail: "none registered under " + s.runnersDir(), Fix: "run `relay add <alias> <owner/repo>`"}}
	}
	var out []Check
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := "runner " + e.Name()
		dir := s.runnerDir(e.Name())
		pathData, err := os.ReadFile(filepath.Join(dir, ".path"))
		if err != nil {
			out = append(out, Check{Name: name, Detail: "not configured (no .path)", Fix: "run `relay add` again for this repository"})
			continue
		}
		var missing []string
		for _, bin := range []string{"claude", "relay"} {
			if !onPath(strings.TrimSpace(string(pathData)), bin) {
				missing = append(missing, bin)
			}
		}
		if len(missing) > 0 {
			out = append(out, Check{Name: name, Detail: "PATH of the service lacks " + strings.Join(missing, ", "), Fix: "re-run `relay add` from a shell where both are on PATH"})
			continue
		}
		plist := s.runnerPlist(e.Name())
		if plist == "" {
			out = append(out, Check{Name: name, Detail: "configured but not installed as a service (no .service file)", Fix: "run `relay add` again for this repository"})
			continue
		}
		data, err := os.ReadFile(plist)
		if err != nil {
			out = append(out, Check{Name: name, Detail: "service plist missing: " + plist, Fix: "run `relay add` again for this repository"})
			continue
		}
		if sessionCreateRe.Match(data) {
			out = append(out, Check{Name: name, Detail: "plist has SessionCreate: the service cannot read the Keychain", Fix: "run `relay add` again, or remove the key and restart the service"})
			continue
		}
		label := strings.TrimSuffix(filepath.Base(plist), ".plist")
		if state := s.agentState(ctx, label); state != "running" {
			out = append(out, Check{Name: name, Detail: "service " + state, Fix: "`launchctl kickstart -k gui/" + s.uid() + "/" + label + "`, or run `relay add` again"})
			continue
		}
		out = append(out, Check{Name: name, OK: true, Detail: "running; PATH has claude and relay; plist ok"})
	}
	return out
}

// runnerPlist finds the LaunchAgent of a runner directory by its .service
// file, which svc.sh writes with the plist's absolute path (older versions
// wrote the bare name).
func (s *Sys) runnerPlist(runner string) string {
	data, err := os.ReadFile(filepath.Join(s.runnerDir(runner), ".service"))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(s.launchAgents(), name)
}

func onPath(path, bin string) bool {
	for _, dir := range strings.Split(path, ":") {
		if dir == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, bin)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

var protocolRe = regexp.MustCompile(`(?m)^\s*RELAY_PROTOCOL:\s*"?(\d+)"?`)

// CheckWorkflow reads the repository's workflow and compares its protocol
// version with this binary's.
func CheckWorkflow(ctx context.Context, cl gh.Client) Check {
	wf, err := cl.FileContent(ctx, ".github/workflows/relay.yml")
	if err != nil {
		return Check{Name: "workflow", Detail: "no .github/workflows/relay.yml on the default branch", Fix: "run /relay:init in the project and push"}
	}
	m := protocolRe.FindStringSubmatch(wf)
	if m == nil {
		return Check{Name: "workflow", Detail: "relay.yml has no RELAY_PROTOCOL", Fix: "regenerate the workflow with /relay:init"}
	}
	if m[1] != strconv.Itoa(protocol.Version) {
		return Check{Name: "workflow", Detail: fmt.Sprintf("workflow is protocol v%s, relay speaks v%d", m[1], protocol.Version), Fix: "regenerate the workflow with /relay:init, or upgrade relay"}
	}
	if _, err := cl.FileContent(ctx, "relay.yml"); err != nil {
		return Check{Name: "workflow", Detail: "relay.yml (the project config) is missing on the default branch", Fix: "run /relay:init in the project and push"}
	}
	return Check{Name: "workflow", OK: true, Detail: fmt.Sprintf("protocol v%s, relay.yml present", m[1])}
}

// CheckIssue inspects one Issue against the protocol: one state label,
// every Relay comment with an ack, error comments with a marker.
func CheckIssue(ctx context.Context, cl gh.Client, n int) []Check {
	issue, err := cl.Issue(ctx, n)
	if err != nil {
		return []Check{{Name: fmt.Sprintf("#%d", n), Detail: err.Error()}}
	}
	var out []Check
	labels, err := protocol.ParseLabels(issue.Labels)
	switch {
	case err != nil:
		out = append(out, Check{Name: "labels", Detail: err.Error(), Fix: "leave exactly one relay:* state label"})
	case labels.Validate() != nil:
		out = append(out, Check{Name: "labels", Detail: "no state label", Fix: "add the relay:* label of the current stage"})
	default:
		out = append(out, Check{Name: "labels", OK: true, Detail: strings.Join(labels.Strings(), " ")})
	}
	relay := 0
	for _, c := range issue.Protocol().Comments {
		if c.Author != protocol.BotLogin || !protocol.IsRelayComment(c.Body) {
			continue
		}
		relay++
		sc, err := protocol.ParseStageComment(c.Body)
		name := fmt.Sprintf("comment %d", c.ID)
		switch {
		case err != nil:
			out = append(out, Check{Name: name, Detail: err.Error()})
		case sc.Stage == protocol.StageError && sc.Error == nil:
			out = append(out, Check{Name: name, Detail: "error comment without relay-error", Fix: "/retry cannot resume from it"})
		case sc.Ack.V == 0:
			out = append(out, Check{Name: name, Detail: string(sc.Stage) + " without relay-ack", Fix: "written by an older relay; harmless, but the watermark ignores it"})
		default:
			out = append(out, Check{Name: name, OK: true, Detail: fmt.Sprintf("%s, ack %d", sc.Stage, sc.Ack.Upto)})
		}
	}
	out = append(out, Check{Name: "watermark", OK: true, Detail: fmt.Sprintf("%d Relay comment(s), watermark %d", relay, protocol.Watermark(issue.Protocol()))})
	return out
}
