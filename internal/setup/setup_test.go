package setup

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// fakeExec answers commands from a table keyed by "name arg1 arg2…" prefix.
type fakeExec struct {
	answers map[string]string // prefix → stdout
	fails   map[string]string // prefix → stderr (exit 1)
	calls   []string
}

func (f *fakeExec) run(_ context.Context, name string, args ...string) (string, string, int, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	for p, stderr := range f.fails {
		if strings.HasPrefix(line, p) {
			return "", stderr, 1, nil
		}
	}
	for p, out := range f.answers {
		if strings.HasPrefix(line, p) {
			return out, "", 0, nil
		}
	}
	return "", "unknown command " + line, 127, nil
}

func newSys(t *testing.T, fe *fakeExec) *Sys {
	t.Helper()
	return &Sys{Exec: fe.run, Home: t.TempDir(), Env: func(string) string { return "" }, Arch: "arm64", Out: &bytes.Buffer{}, Version: "0.1.0"}
}

const ghUserOK = "HTTP/2.0 200 OK\nX-Oauth-Scopes: admin:public_key, gist, read:org, repo, workflow\n\n{\"login\":\"masteine\"}"

func realNotes(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "runner", "release-notes-v2.338.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func goodExec() *fakeExec {
	return &fakeExec{answers: map[string]string{
		"claude --version":          "2.1.281 (Claude Code)\n",
		"claude auth status":        `{"loggedIn":true,"authMethod":"claude.ai","email":"x@y"}`,
		"gh api user -i":            ghUserOK,
		"launchctl getenv":          "",
		"launchctl print":           "x = {\n\tstate = running\n\tlast exit code = 0\n}\n",
		"claude plugin list":        "Installed plugins:\n\n  ❯ relay@relay\n    Version: 0.1.0\n    Scope: user\n",
		"claude plugin marketplace": "",
		"claude plugin install":     "",
		"security add-generic":      "",
		"launchctl bootout":         "",
		"launchctl bootstrap":       "",
		"gh api users/masteine":     "User\n",
		"gh api -X POST repos/":     "AAAA-token\n",
		"gh release download":       "",
		"tar -xzf":                  "",
		"/usr/bin/env -i":           "",
	}, fails: map[string]string{}}
}

func TestChecksHappyPath(t *testing.T) {
	s := newSys(t, goodExec())
	ctx := context.Background()
	if c := s.CheckClaude(ctx); !c.OK || !strings.Contains(c.Detail, "claude.ai") {
		t.Errorf("claude: %+v", c)
	}
	login, c := s.CheckGH(ctx)
	if !c.OK || login != "masteine" {
		t.Errorf("gh: %s %+v", login, c)
	}
	if c := s.CheckAPIKey(ctx); !c.OK {
		t.Errorf("api key: %+v", c)
	}
	if c := s.CheckPlugin(ctx); !c.OK || !strings.Contains(c.Detail, "0.1.0") {
		t.Errorf("plugin: %+v", c)
	}
}

func TestChecksFailures(t *testing.T) {
	ctx := context.Background()
	fe := goodExec()
	fe.answers["claude --version"] = "2.0.9 (Claude Code)\n"
	if c := newSys(t, fe).CheckClaude(ctx); c.OK || !strings.Contains(c.Fix, MinClaude) {
		t.Errorf("old claude: %+v", c)
	}
	fe = goodExec()
	fe.answers["claude auth status"] = `{"loggedIn":false,"authMethod":"none"}`
	if c := newSys(t, fe).CheckClaude(ctx); c.OK || !strings.Contains(c.Fix, "claude auth login") {
		t.Errorf("not logged in: %+v", c)
	}
	fe = goodExec()
	fe.answers["claude auth status"] = `{"loggedIn":true,"authMethod":"apiKey"}`
	if c := newSys(t, fe).CheckClaude(ctx); c.OK {
		t.Errorf("api key auth: %+v", c)
	}
	fe = goodExec()
	fe.answers["gh api user -i"] = "HTTP/2.0 200 OK\nX-Oauth-Scopes: repo\n\n{\"login\":\"masteine\"}"
	if login, c := newSys(t, fe).CheckGH(ctx); c.OK || login != "masteine" || !strings.Contains(c.Detail, "workflow") {
		t.Errorf("missing scope: %s %+v", login, c)
	}
	fe = goodExec()
	fe.answers["gh api user -i"] = "HTTP/2.0 200 OK\n\n{\"login\":\"masteine\"}"
	if _, c := newSys(t, fe).CheckGH(ctx); !c.OK || !strings.Contains(c.Detail, "cannot be verified") {
		t.Errorf("fine-grained token: %+v", c)
	}
	fe = goodExec()
	fe.fails["gh api user -i"] = "gh: not logged in"
	if _, c := newSys(t, fe).CheckGH(ctx); c.OK {
		t.Errorf("gh failure: %+v", c)
	}
	fe = goodExec()
	fe.answers["launchctl getenv"] = "sk-ant-x\n"
	if c := newSys(t, fe).CheckAPIKey(ctx); c.OK || !strings.Contains(c.Detail, "launchd") {
		t.Errorf("launchd key: %+v", c)
	}
	s := newSys(t, goodExec())
	s.Env = func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk"
		}
		return ""
	}
	if c := s.CheckAPIKey(ctx); c.OK {
		t.Errorf("shell key: %+v", c)
	}
	fe = goodExec()
	fe.answers["claude plugin list"] = "Installed plugins:\n\n  ❯ caveman@caveman\n"
	if c := newSys(t, fe).CheckPlugin(ctx); c.OK {
		t.Errorf("plugin missing: %+v", c)
	}
	// Version skew between the plugin and the binary.
	fe = goodExec()
	fe.answers["claude plugin list"] = "  ❯ relay@relay\n    Version: 0.2.0\n"
	if c := newSys(t, fe).CheckPlugin(ctx); c.OK || !strings.Contains(c.Detail, "0.2.0") {
		t.Errorf("skew: %+v", c)
	}
	s = newSys(t, fe)
	s.Version = "dev"
	if c := s.CheckPlugin(ctx); !c.OK {
		t.Errorf("dev binary does not compare: %+v", c)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, min string
		want      bool
	}{
		{"2.1.281 (Claude Code)", "2.1.281", true}, {"2.1.300", "2.1.281", true}, {"2.2.0", "2.1.281", true}, {"3.0.0", "2.1.281", true},
		{"2.1.280", "2.1.281", false}, {"1.9.999", "2.1.281", false}, {"garbage", "2.1.281", false},
	} {
		if got := versionAtLeast(tc.have, tc.min); got != tc.want {
			t.Errorf("versionAtLeast(%q, %q) = %v", tc.have, tc.min, got)
		}
	}
}

func TestBotConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay", "config.yml")
	if _, found, err := ReadBotConfig(path); err != nil || found {
		t.Fatalf("missing file: found=%v err=%v", found, err)
	}
	if _, err := AddAlias(path, "Todo", gh.Repo{Owner: "masteine", Name: "relay-sandbox"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"my.app", "a b", "", strings.Repeat("x", 33), "x\ny"} {
		if _, err := AddAlias(path, bad, gh.Repo{Owner: "a", Name: "b"}); err == nil {
			t.Errorf("alias %q must be refused", bad)
		}
	}
	if _, err := AllowPrincipal(path, "-100123", "7"); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowPrincipal(path, "-100123", "7"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"abc", "7"}, {"1", ""}, {"1\nallow:", "2"}} {
		if _, err := AllowPrincipal(path, bad[0], bad[1]); err == nil {
			t.Errorf("ids %q must be refused", bad)
		}
	}
	data, _ := os.ReadFile(path)
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", st.Mode().Perm())
	}
	if !strings.Contains(string(data), `todo: "masteine/relay-sandbox"`) || !strings.Contains(string(data), `{chat: "-100123", user: "7"}`) || !strings.Contains(string(data), "poll: 20s") {
		t.Errorf("file:\n%s", data)
	}
	c, err := core.LoadConfig(path)
	if err != nil {
		t.Fatalf("the written file must load and validate: %v", err)
	}
	if len(c.Allow) != 1 || !c.Allowed("-100123", "7") || c.Poll.String() != "20s" {
		t.Errorf("config = %+v", c)
	}
	if _, err := AddAlias(path, "todo", gh.Repo{Owner: "other", Name: "repo"}); err == nil {
		t.Error("re-pointing an alias must be refused")
	}
	if _, err := AddAlias(path, "todo", gh.Repo{Owner: "Masteine", Name: "relay-sandbox"}); err != nil {
		t.Errorf("same repo, other case: %v", err)
	}
}

func TestBotPlistAndSessionCreate(t *testing.T) {
	p := BotPlist("/usr/local/bin/relay", "/Users/x/.config/relay/config.yml", "/Users/x/Library/Logs/relay", "/a:/b")
	for _, want := range []string{"<string>dev.relay.bot</string>", "<string>bot</string>", "<string>--config</string>", "<key>KeepAlive</key>", "/Users/x/Library/Logs/relay/bot.log", "<string>/a:/b</string>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if _, err := exec.LookPath("plutil"); err == nil {
		f := filepath.Join(t.TempDir(), "bot.plist")
		_ = os.WriteFile(f, []byte(p), 0o644)
		if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
			t.Errorf("plutil -lint: %v\n%s", err, out)
		}
	}
	path := filepath.Join(t.TempDir(), "r.plist")
	const runnerPlist = "<dict>\n    <key>RunAtLoad</key>\n    <true/>\n    <key>ProcessType</key>\n    <string>Interactive</string>\n    <key>SessionCreate</key>\n    <true/>\n</dict>"
	if err := os.WriteFile(path, []byte(runnerPlist), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := StripSessionCreate(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "SessionCreate") || !strings.Contains(string(data), "<key>ProcessType</key>\n    <string>Interactive</string>\n</dict>") {
		t.Errorf("after strip:\n%s", data)
	}
	if changed, _ := StripSessionCreate(path); changed {
		t.Error("second strip must be a no-op")
	}
}

func TestRunnerAssetAndSHA(t *testing.T) {
	if RunnerAsset("v2.338.0", "arm64") != "actions-runner-osx-arm64-2.338.0.tar.gz" || RunnerAsset("v2.338.0", "amd64") != "actions-runner-osx-x64-2.338.0.tar.gz" {
		t.Error("RunnerAsset")
	}
	// The real notes: the asset name appears in curl/tar examples first,
	// and other assets' hashes come before its own.
	body := realNotes(t)
	if got := ParseSHA(body, "actions-runner-osx-arm64-2.338.0.tar.gz"); got != "df4cebda25c86a886ed204e49fee63f5c2e7cec5f447b5c98440a826bbdf9df2" {
		t.Errorf("arm64 sha = %s", got)
	}
	if got := ParseSHA(body, "actions-runner-osx-x64-2.338.0.tar.gz"); got != "dea7a58796ce215fc424a8b27c0fdd9b813fa5fc03d109a4a1d38131d6bfa1a3" {
		t.Errorf("x64 sha = %s", got)
	}
	if ParseSHA(body, "nope.tar.gz") != "" {
		t.Error("unknown asset")
	}
	if ParseSHA("actions-runner-osx-arm64-2.338.0.tar.gz\n"+strings.Repeat("a", 64), "actions-runner-osx-arm64-2.338.0.tar.gz") != "" {
		t.Error("a hash without the BEGIN SHA marker must not be trusted")
	}
}

func TestEnsureDistVerifiesSHA(t *testing.T) {
	fe := goodExec()
	// Notes whose arm64 hash is that of an empty file.
	fe.answers["gh api repos/actions/runner/releases/latest"] = `{"tag_name":"v2.338.0","body":"- actions-runner-osx-arm64-2.338.0.tar.gz <!-- BEGIN SHA osx-arm64 -->e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855<!-- END SHA osx-arm64 -->"}`
	s := newSys(t, fe)
	dist := filepath.Join(s.Home, ".relay", "runner-dist", "actions-runner-osx-arm64-2.338.0.tar.gz")
	orig := fe.run
	s.Exec = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		if name == "gh" && len(args) > 1 && args[0] == "release" {
			_ = os.MkdirAll(filepath.Dir(dist), 0o755)
			_ = os.WriteFile(dist, nil, 0o644)
		}
		return orig(ctx, name, args...)
	}
	path, tag, err := s.EnsureDist(context.Background())
	if err != nil || path != dist || tag != "v2.338.0" {
		t.Fatalf("path=%s tag=%s err=%v", path, tag, err)
	}
	n := len(fe.calls)
	if _, _, err := s.EnsureDist(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range fe.calls[n:] {
		if strings.HasPrefix(c, "gh release download") {
			t.Error("must not download again")
		}
	}
	_ = os.WriteFile(dist, []byte("tampered"), 0o644)
	s.Exec = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		if name == "gh" && len(args) > 1 && args[0] == "release" {
			_ = os.WriteFile(dist, []byte("still wrong"), 0o644)
		}
		return orig(ctx, name, args...)
	}
	if _, _, err := s.EnsureDist(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("tampered: %v", err)
	}
	if _, err := os.Stat(dist); err == nil {
		t.Error("a file with a wrong SHA must be removed")
	}
}

// runnerFakes makes tar, config.sh and svc.sh leave the files the real ones
// leave: config.sh → .runner and .path, svc.sh install → .service with the
// plist's absolute path and the plist itself (with SessionCreate).
func runnerFakes(s *Sys, fe *fakeExec, dir, plist string) {
	orig := fe.run
	s.Exec = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		line := strings.Join(args, " ")
		switch {
		case name == "tar":
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "config.sh"), []byte("#!/bin/sh"), 0o755)
		case strings.Contains(line, "./config.sh --unattended"):
			_ = os.WriteFile(filepath.Join(dir, ".runner"), []byte("\ufeff{\n  \"agentName\": \"x\",\n  \"gitHubUrl\": \"https://github.com/masteine/relay-sandbox\"\n}"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, ".path"), []byte(filepath.Join(s.Home, "bin")+":/usr/bin\n"), 0o644)
		case strings.Contains(line, "./svc.sh install"):
			_ = os.MkdirAll(filepath.Dir(plist), 0o755)
			_ = os.WriteFile(plist, []byte("<dict><key>SessionCreate</key><true/></dict>"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, ".service"), []byte(plist+"\n"), 0o644)
		}
		return orig(ctx, name, args...)
	}
}

func TestRegisterRunnerFlowAndRerun(t *testing.T) {
	fe := goodExec()
	s := newSys(t, fe)
	repo := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	dir := filepath.Join(s.Home, ".relay", "runners", "masteine-relay-sandbox")
	plist := filepath.Join(s.Home, "Library", "LaunchAgents", "actions.runner.masteine-relay-sandbox.x.plist")
	runnerFakes(s, fe, dir, plist)
	if err := s.RegisterRunner(context.Background(), repo, "/tmp/runner.tar.gz"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fe.calls, "\n")
	for _, want := range []string{
		"gh api -X POST repos/masteine/relay-sandbox/actions/runners/registration-token",
		`/bin/sh -c cd "$1" && shift && exec "$@" relay ` + dir + " ./config.sh --unattended --url https://github.com/masteine/relay-sandbox --token AAAA-token --labels relay --name relay-",
		"/usr/bin/env -i HOME=" + s.Home,
		" relay " + dir + " ./svc.sh install",
		" relay " + dir + " ./svc.sh start",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("calls lack %q:\n%s", want, joined)
		}
	}
	data, _ := os.ReadFile(plist)
	if strings.Contains(string(data), "SessionCreate") {
		t.Error("SessionCreate must be stripped")
	}

	// Running add again for the same repository: no token, no config.sh,
	// no second svc.sh install, just a restart.
	fe.calls = nil
	if err := s.RegisterRunner(context.Background(), repo, "/tmp/runner.tar.gz"); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	joined = strings.Join(fe.calls, "\n")
	for _, no := range []string{"registration-token", "config.sh", "svc.sh install"} {
		if strings.Contains(joined, no) {
			t.Errorf("re-run must not call %s:\n%s", no, joined)
		}
	}
	if !strings.Contains(joined, "./svc.sh start") {
		t.Error("re-run must restart the service")
	}
	// A directory holding another repository's runner is refused.
	other := gh.Repo{Owner: "masteine", Name: "relay-sandbox"}
	_ = os.WriteFile(filepath.Join(dir, ".runner"), []byte(`{"gitHubUrl":"https://github.com/someone/else"}`), 0o644)
	if err := s.RegisterRunner(context.Background(), other, "/tmp/runner.tar.gz"); err == nil || !strings.Contains(err.Error(), "someone/else") {
		t.Errorf("mismatch: %v", err)
	}
	// A legacy directory named after the repository alone is reused.
	legacy := filepath.Join(s.Home, ".relay", "runners", "relay-sandbox")
	_ = os.MkdirAll(legacy, 0o755)
	_ = os.WriteFile(filepath.Join(legacy, ".runner"), []byte(`{"gitHubUrl":"https://github.com/masteine/relay-sandbox"}`), 0o644)
	_ = os.Remove(filepath.Join(dir, ".runner"))
	if got := s.runnerDirFor(repo); got != legacy {
		t.Errorf("runnerDirFor = %s, want the legacy dir", got)
	}
}

func TestCheckRunners(t *testing.T) {
	fe := goodExec()
	s := newSys(t, fe)
	ctx := context.Background()
	if cs := s.CheckRunners(ctx); len(cs) != 1 || cs[0].OK {
		t.Errorf("no runners: %+v", cs)
	}
	dir := filepath.Join(s.Home, ".relay", "runners", "masteine-relay-sandbox")
	plist := filepath.Join(s.Home, "Library", "LaunchAgents", "actions.runner.masteine-relay-sandbox.x.plist")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".path"), []byte(filepath.Join(s.Home, "bin")+":/usr/bin\n"), 0o644)
	if cs := s.CheckRunners(ctx); cs[0].OK || !strings.Contains(cs[0].Detail, "lacks claude, relay") {
		t.Errorf("without binaries: %+v", cs)
	}
	_ = os.MkdirAll(filepath.Join(s.Home, "bin"), 0o755)
	for _, b := range []string{"claude", "relay"} {
		_ = os.WriteFile(filepath.Join(s.Home, "bin", b), []byte("#!/bin/sh"), 0o755)
	}
	if cs := s.CheckRunners(ctx); cs[0].OK || !strings.Contains(cs[0].Detail, "no .service") {
		t.Errorf("without .service: %+v", cs)
	}
	_ = os.WriteFile(filepath.Join(dir, ".service"), []byte(plist+"\n"), 0o644)
	if cs := s.CheckRunners(ctx); cs[0].OK || !strings.Contains(cs[0].Detail, "plist missing") {
		t.Errorf("plist missing: %+v", cs)
	}
	_ = os.MkdirAll(filepath.Dir(plist), 0o755)
	_ = os.WriteFile(plist, []byte("<dict><key>SessionCreate</key><true/></dict>"), 0o644)
	if cs := s.CheckRunners(ctx); cs[0].OK || !strings.Contains(cs[0].Detail, "SessionCreate") {
		t.Errorf("SessionCreate: %+v", cs)
	}
	_ = os.WriteFile(plist, []byte("<dict></dict>"), 0o644)
	fe.answers["launchctl print"] = "x = {\n\tstate = not running\n\tlast exit code = 1\n}\n"
	if cs := s.CheckRunners(ctx); cs[0].OK || !strings.Contains(cs[0].Detail, "not running, last exit 1") {
		t.Errorf("not running: %+v", cs)
	}
	fe.answers["launchctl print"] = "x = {\n\tstate = running\n}\n"
	if cs := s.CheckRunners(ctx); !cs[0].OK {
		t.Errorf("running: %+v", cs)
	}
}

func TestAcceptable(t *testing.T) {
	since := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	ok := transport.Incoming{Chat: "1", User: "2", Private: true, At: since.Add(time.Second)}
	if !Acceptable(ok, since) {
		t.Error("private, fresh message must be accepted")
	}
	for name, in := range map[string]transport.Incoming{
		"group":     {Chat: "-1", User: "2", Private: false, At: since.Add(time.Second)},
		"backlog":   {Chat: "1", User: "2", Private: true, At: since.Add(-time.Hour)},
		"no user":   {Chat: "1", Private: true},
		"no chat":   {User: "2", Private: true},
		"zero time": {Chat: "1", User: "2", Private: true}, // accepted: unknown time is not rejected
	} {
		got := Acceptable(in, since)
		if name == "zero time" {
			if !got {
				t.Error("unknown time must not be rejected")
			}
			continue
		}
		if got {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestSetupAndDoctor(t *testing.T) {
	fe := goodExec()
	fe.answers["claude plugin list"] = "Installed plugins:\n" // not installed at first
	s := newSys(t, fe)
	s.Exec = func(ctx context.Context, name string, args ...string) (string, string, int, error) {
		if name == "claude" && len(args) > 1 && args[0] == "plugin" && args[1] == "install" {
			fe.answers["claude plugin list"] = "  ❯ relay@relay\n    Version: 0.1.0\n"
		}
		return fe.run(ctx, name, args...)
	}
	var captured string
	err := s.Setup(context.Background(), Options{
		Binary:   "/opt/homebrew/bin/relay",
		AskToken: func() (string, error) { return "123:abc", nil },
		Capture: func(_ context.Context, token string, confirm func(string) bool, _ io.Writer) (Sender, error) {
			captured = token
			if confirm != nil && !confirm("test?") {
				t.Error("confirm answered no")
			}
			return Sender{Chat: "100", User: "7", BotName: "relaytest_bot"}, nil
		},
		Confirm: func(string) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured != "123:abc" {
		t.Error("capture must use the entered token before it is stored")
	}
	joined := strings.Join(fe.calls, "\n")
	for _, want := range []string{"claude plugin marketplace add masteine/relay", "claude plugin install relay@relay --scope user", "security add-generic-password -a relay -s relay-telegram -U -w 123:abc", "launchctl bootstrap gui/"} {
		if !strings.Contains(joined, want) {
			t.Errorf("calls lack %q", want)
		}
	}
	// The token is stored after the capture validated it.
	if strings.Index(joined, "security add-generic") < 0 {
		t.Fatal("token not stored")
	}
	cfg, found, err := ReadBotConfig(s.configPath())
	if err != nil || !found || !cfg.Allowed("100", "7") {
		t.Errorf("config after setup: found=%v err=%v %+v", found, err, cfg)
	}
	if _, err := os.Stat(s.botPlist()); err != nil {
		t.Error("bot plist not written")
	}
	// Re-run with --no-token semantics: stored token found via the file
	// fallback here, allowlist kept, no second capture.
	tokenFile := filepath.Join(filepath.Dir(s.configPath()), "telegram.token")
	_ = os.WriteFile(tokenFile, []byte("123:abc\n"), 0o600)
	cfg.Telegram.TokenFile = tokenFile
	_ = WriteBotConfig(s.configPath(), cfg)
	captures, checks := 0, 0
	err = s.Setup(context.Background(), Options{Binary: "/x", Capture: func(context.Context, string, func(string) bool, io.Writer) (Sender, error) {
		captures++
		return Sender{}, nil
	}})
	if err != nil || captures != 0 {
		t.Errorf("re-run: err=%v captures=%d", err, captures)
	}
	// A new token on a re-run (allowlist present, so no capture) is validated before it is stored.
	err = s.Setup(context.Background(), Options{Binary: "/x",
		AskToken:   func() (string, error) { return "999:new", nil },
		CheckToken: func(context.Context, string) error { checks++; return nil },
		Capture: func(context.Context, string, func(string) bool, io.Writer) (Sender, error) {
			captures++
			return Sender{}, nil
		}})
	if err != nil || checks != 1 || captures != 0 {
		t.Errorf("re-run with a new token: err=%v checks=%d captures=%d", err, checks, captures)
	}
	// Doctor: everything but runners is fine.
	out := &bytes.Buffer{}
	s.Out = out
	if ok := s.Doctor(context.Background(), nil, 0); ok {
		t.Error("doctor must fail without runners")
	}
	if !strings.Contains(out.String(), "✘ runners") || !strings.Contains(out.String(), "✔ bot: dev.relay.bot running") {
		t.Errorf("doctor output:\n%s", out.String())
	}
	// A failed check stops setup.
	fe.answers["claude auth status"] = `{"loggedIn":false}`
	if err := s.Setup(context.Background(), Options{Binary: "/x"}); err == nil {
		t.Error("setup must stop on a failed check")
	}
}

func TestServiceBinary(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/homebrew/Cellar/relay/0.1.0/bin/relay": "/opt/homebrew/opt/relay/bin/relay",
		"/usr/local/Cellar/relay/1.2.3/bin/relay":    "/usr/local/opt/relay/bin/relay",
		"/opt/homebrew/bin/relay":                    "/opt/homebrew/bin/relay",
		"/Users/x/.local/bin/relay":                  "/Users/x/.local/bin/relay",
	} {
		if got := ServiceBinary(in); got != want {
			t.Errorf("ServiceBinary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBotConfigQuotesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	c := core.Defaults()
	c.Aliases = map[string]string{"todo": "masteine/relay-sandbox"}
	c.Allow = []core.Principal{{Chat: "1", User: "2"}}
	c.Telegram.TokenFile = "/Users/x/odd: name #1/token"
	c.Workflow = "relay #2.yml"
	if err := WriteBotConfig(path, c); err != nil {
		t.Fatal(err)
	}
	back, err := core.LoadConfig(path)
	if err != nil {
		t.Fatalf("quoted values must load: %v", err)
	}
	if back.Telegram.TokenFile != c.Telegram.TokenFile || back.Workflow != c.Workflow {
		t.Errorf("round trip: %+v", back.Telegram)
	}
}

func TestInstallPluginConflict(t *testing.T) {
	fe := goodExec()
	fe.fails["claude plugin marketplace add"] = `Failed to add marketplace: Cannot add marketplace "relay": its network source differs from the one declared for it in settings`
	err := newSys(t, fe).InstallPlugin(context.Background(), "masteine/relay")
	if err == nil || !strings.Contains(err.Error(), "claude plugin marketplace remove relay") {
		t.Errorf("err = %v", err)
	}
	fe = goodExec()
	fe.fails["claude plugin marketplace add"] = "marketplace relay already exists"
	if err := newSys(t, fe).InstallPlugin(context.Background(), "masteine/relay"); err != nil {
		t.Errorf("already registered is fine: %v", err)
	}
}

func TestSetupWithoutTokenStops(t *testing.T) {
	fe := goodExec()
	s := newSys(t, fe)
	t.Setenv("RELAY_TELEGRAM_TOKEN", "")
	fe.fails["security find-generic-password"] = "not found"
	if err := s.Setup(context.Background(), Options{Binary: "/x", Capture: func(context.Context, string, func(string) bool, io.Writer) (Sender, error) {
		t.Fatal("must not capture without a token")
		return Sender{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.botPlist()); err == nil {
		t.Error("no bot service without a token")
	}
}

func TestCheckWorkflowAndIssue(t *testing.T) {
	f := gh.NewFake()
	if c := CheckWorkflow(context.Background(), f); c.OK {
		t.Errorf("missing workflow: %+v", c)
	}
	f.Files[".github/workflows/relay.yml"] = "env:\n  RELAY_PROTOCOL: \"9\"\n"
	if c := CheckWorkflow(context.Background(), f); c.OK || !strings.Contains(c.Detail, "v9") {
		t.Errorf("protocol mismatch: %+v", c)
	}
	f.Files[".github/workflows/relay.yml"] = "env:\n  RELAY_PROTOCOL: \"1\"\n"
	if c := CheckWorkflow(context.Background(), f); c.OK || !strings.Contains(c.Detail, "relay.yml") {
		t.Errorf("missing project config: %+v", c)
	}
	f.Files["relay.yml"] = "owner: masteine\n"
	if c := CheckWorkflow(context.Background(), f); !c.OK {
		t.Errorf("ok: %+v", c)
	}

	ok, _ := protocol.FormatStageComment(protocol.StageComment{Stage: protocol.StagePlan, Body: "x", Ack: protocol.Ack{Upto: 5}})
	bad := "## Relay · error\n\nno marker\n"
	f = gh.NewFake(
		gh.Issue{Number: 1, Labels: []string{"relay", "relay:plan", "needs-approval"}, Comments: []gh.Comment{{ID: 10, Author: protocol.BotLogin, Body: ok}}},
		gh.Issue{Number: 2, Labels: []string{"relay:plan", "relay:triage"}, Comments: []gh.Comment{{ID: 11, Author: protocol.BotLogin, Body: bad}}},
	)
	if cs := CheckIssue(context.Background(), f, 1); !AllOK(cs) {
		t.Errorf("healthy issue: %+v", cs)
	}
	cs := CheckIssue(context.Background(), f, 2)
	if AllOK(cs) || !strings.Contains(cs[0].Detail, "more than one state label") || !strings.Contains(cs[1].Detail, "without relay-error") {
		t.Errorf("broken issue: %+v", cs)
	}
	if cs := CheckIssue(context.Background(), f, 9); AllOK(cs) {
		t.Error("missing issue")
	}
}
