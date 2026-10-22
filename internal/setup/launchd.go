package setup

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// BotLabel is the bot's LaunchAgent label.
const BotLabel = "dev.relay.bot"

// BotPlist renders the LaunchAgent that keeps the bot running: started at
// login, restarted when it exits, logs under ~/Library/Logs/relay.
func BotPlist(binary, configPath, logDir, pathEnv string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>bot</string>
        <string>--config</string>
        <string>%s</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>30</integer>
    <key>StandardOutPath</key>
    <string>%s/bot.log</string>
    <key>StandardErrorPath</key>
    <string>%s/bot.log</string>
</dict>
</plist>
`, BotLabel, html.EscapeString(binary), html.EscapeString(configPath), html.EscapeString(pathEnv), html.EscapeString(logDir), html.EscapeString(logDir))
}

// InstallBotService writes the plist and (re)starts the agent.
func (s *Sys) InstallBotService(ctx context.Context, binary string) error {
	if err := os.MkdirAll(s.logsDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.launchAgents(), 0o755); err != nil {
		return err
	}
	pathEnv := filepath.Dir(binary) + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
	if err := os.WriteFile(s.botPlist(), []byte(BotPlist(binary, s.configPath(), s.logsDir(), pathEnv)), 0o644); err != nil {
		return err
	}
	return s.restartAgent(ctx, s.botPlist(), BotLabel)
}

// restartAgent boots an agent out (ignoring "not loaded") and bootstraps
// it. bootout is asynchronous, so a bootstrap right after it can fail with
// "service already loaded"; it is retried briefly.
func (s *Sys) restartAgent(ctx context.Context, plist, label string) error {
	domain := "gui/" + s.uid()
	_, _, _, _ = s.Exec(ctx, "launchctl", "bootout", domain+"/"+label)
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = s.run(ctx, "launchctl", "bootstrap", domain, plist); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return fmt.Errorf("start %s: %w", label, err)
}

func (s *Sys) uid() string {
	if u, err := user.Current(); err == nil {
		return u.Uid
	}
	return fmt.Sprint(os.Getuid())
}

// sessionCreateRe matches the key svc.sh puts into a runner's plist. With
// it, the service runs in a fresh security session and cannot read the
// login Keychain, so `claude` is "not logged in" (spike S1).
var sessionCreateRe = regexp.MustCompile(`(?s)\s*<key>SessionCreate</key>\s*<(true|false)\s*/>`)

// StripSessionCreate removes the key from a plist file; it reports whether
// the file changed.
func StripSessionCreate(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if !sessionCreateRe.Match(data) {
		return false, nil
	}
	out := sessionCreateRe.ReplaceAll(data, nil)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// cleanEnvArgs wraps a command so that it runs inside dir with a minimal
// environment: the runner must not inherit CLAUDE_*/ANTHROPIC_* from the
// desktop app, and svc.sh insists on being run from the runner's root. The
// directory and the arguments travel as positional parameters, never
// through shell quoting.
func (s *Sys) cleanEnvArgs(dir, name string, args ...string) (string, []string) {
	u := s.Env("USER")
	if u == "" {
		u = "runner"
	}
	path := strings.Join([]string{filepath.Join(s.Home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	full := []string{"-i", "HOME=" + s.Home, "USER=" + u, "LOGNAME=" + u, "SHELL=/bin/zsh", "LANG=en_US.UTF-8", "PATH=" + path,
		"/bin/sh", "-c", `cd "$1" && shift && exec "$@"`, "relay", dir, name}
	return "/usr/bin/env", append(full, args...)
}
