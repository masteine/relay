package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/bot/telegram"
	"github.com/masteine/relay/internal/gh"
)

// DefaultMarketplace is the GitHub repository that hosts the plugin.
const DefaultMarketplace = "masteine/relay"

// InstallPlugin adds the marketplace and installs the plugin for the user.
// Both steps are idempotent.
func (s *Sys) InstallPlugin(ctx context.Context, marketplace string) error {
	if out, err := s.run(ctx, "claude", "plugin", "marketplace", "add", marketplace); err != nil && !strings.Contains(strings.ToLower(out+err.Error()), "already") {
		if strings.Contains(out+err.Error(), "differs from the one declared") {
			return fmt.Errorf("a marketplace named relay is already registered from another source; run `claude plugin marketplace remove relay` and then `relay setup` again")
		}
		return fmt.Errorf("add marketplace %s: %w", marketplace, err)
	}
	if out, err := s.run(ctx, "claude", "plugin", "install", PluginRef, "--scope", "user"); err != nil && !strings.Contains(strings.ToLower(out+err.Error()), "already") {
		return fmt.Errorf("install %s: %w", PluginRef, err)
	}
	return nil
}

// StoreToken puts the Telegram token into the login Keychain under the
// service name from the config. When the Keychain refuses (a locked SSH
// session, say), the token goes to a 0600 file next to the config and the
// config points at it. It returns the token file path, or "".
func (s *Sys) StoreToken(ctx context.Context, service, token string) (string, error) {
	_, err := s.run(ctx, "security", "add-generic-password", "-a", "relay", "-s", service, "-U", "-w", token)
	if err == nil {
		return "", nil
	}
	s.logf("Keychain refused the token (%v); falling back to a file", err)
	file := filepath.Join(filepath.Dir(s.configPath()), "telegram.token")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return file, nil
}

// Options drive Setup; the interactive parts are injected so that tests
// and non-interactive runs can supply them.
type Options struct {
	Marketplace string
	// Binary is the relay executable to put into the LaunchAgent; empty
	// means this executable.
	Binary string
	// AskToken returns the Telegram bot token; nil keeps the stored one.
	AskToken func() (string, error)
	// Capture waits for the owner's first private message to the bot and
	// returns who sent it; nil skips the allowlist step.
	Capture func(ctx context.Context, token string, confirm func(string) bool, out io.Writer) (Sender, error)
	// Confirm asks a yes/no question; nil means yes.
	Confirm func(prompt string) bool
	// CheckToken validates a token (getMe) when Capture does not run; nil
	// skips the check.
	CheckToken func(ctx context.Context, token string) error
}

// Setup prepares the machine once: checks, plugin, token, allowlist, bot
// service. It stops at the first failed check. Re-running it is safe: a
// stored token and an existing allowlist are kept.
func (s *Sys) Setup(ctx context.Context, o Options) error {
	if o.Marketplace == "" {
		o.Marketplace = DefaultMarketplace
	}
	s.logf("relay setup")
	checks := []Check{s.CheckClaude(ctx), s.CheckAPIKey(ctx)}
	_, ghCheck := s.CheckGH(ctx)
	checks = append(checks, ghCheck)
	for _, c := range checks {
		s.logf("%s", c)
	}
	if !AllOK(checks) {
		return errors.New("fix the checks above and run relay setup again")
	}

	if c := s.CheckPlugin(ctx); !c.OK {
		s.logf("installing the plugin from %s…", o.Marketplace)
		if err := s.InstallPlugin(ctx, o.Marketplace); err != nil {
			return err
		}
	}
	s.logf("%s", s.CheckPlugin(ctx))

	cfg, _, err := ReadBotConfig(s.configPath())
	if err != nil {
		return err
	}
	// The token: asked now, or already stored.
	var token string
	if o.AskToken != nil {
		if token, err = o.AskToken(); err != nil {
			return err
		}
		if token = strings.TrimSpace(token); token == "" {
			return errors.New("empty token")
		}
	} else if token, err = telegram.Token(cfg.Telegram.Keychain, cfg.Telegram.TokenFile); err != nil {
		s.logf("no Telegram token stored yet; run `relay setup` without --no-token when you have one from @BotFather")
		return s.finish(cfg, false)
	}
	// The allowlist: captured from the first private message, which also
	// validates the token before it is stored.
	if o.Capture != nil && len(cfg.Allow) == 0 {
		sender, err := o.Capture(ctx, token, o.Confirm, s.Out)
		if err != nil {
			return err
		}
		if _, err := AllowPrincipal(s.configPath(), core.NameTelegram, sender.Chat, sender.User); err != nil {
			return err
		}
		s.logf("allowed user %s in chat %s (bot @%s)", sender.User, sender.Chat, sender.BotName)
	}
	if o.AskToken != nil {
		if len(cfg.Allow) > 0 && o.CheckToken != nil {
			if err := o.CheckToken(ctx, token); err != nil {
				return fmt.Errorf("the token does not work: %w", err)
			}
		}
		file, err := s.StoreToken(ctx, cfg.Telegram.Keychain, token)
		if err != nil {
			return fmt.Errorf("store the token: %w", err)
		}
		if file != "" {
			cfg.Telegram.TokenFile = file
			s.logf("the Keychain refused; token stored in %s (mode 0600)", file)
		} else {
			s.logf("token stored in the Keychain (%s)", cfg.Telegram.Keychain)
		}
	}
	cfg2, _, err := ReadBotConfig(s.configPath())
	if err != nil {
		return err
	}
	cfg2.Telegram.TokenFile = cfg.Telegram.TokenFile
	if err := WriteBotConfig(s.configPath(), cfg2); err != nil {
		return err
	}
	if len(cfg2.Allow) == 0 {
		s.logf("no allowed chat yet, so the bot service is not installed: run `relay setup` again and send the bot a message")
		return s.finish(cfg2, false)
	}

	if o.Binary == "" {
		o.Binary = s.selfBinary()
	}
	if err := s.InstallBotService(ctx, o.Binary); err != nil {
		return err
	}
	s.logf("bot service %s installed; logs in %s", BotLabel, s.logsDir())
	return s.finish(cfg2, true)
}

// selfBinary is the executable to run as the service: this one, or the one
// on PATH when this one cannot be resolved. Symlinks are not resolved, and a
// Homebrew keg path is mapped to the stable opt/ path: the keg disappears on
// `brew upgrade`, and a plist pointing at it would leave the bot dead.
func (s *Sys) selfBinary() string {
	p, err := os.Executable()
	if err != nil {
		if p, err = exec.LookPath("relay"); err != nil {
			return "relay"
		}
	}
	return ServiceBinary(p)
}

var cellarRe = regexp.MustCompile(`^(.*)/Cellar/relay/[^/]+/bin/relay$`)

// ServiceBinary turns a Homebrew keg path (…/Cellar/relay/0.1.0/bin/relay)
// into the stable …/opt/relay/bin/relay; other paths are returned as is.
func ServiceBinary(path string) string {
	if m := cellarRe.FindStringSubmatch(path); m != nil {
		return m[1] + "/opt/relay/bin/relay"
	}
	return path
}

func (s *Sys) finish(cfg core.Config, botInstalled bool) error {
	s.logf("")
	s.logf("Next: in each project run /relay:init inside Claude Code, then `relay add <alias> <owner/repo>`.")
	if botInstalled && len(cfg.Aliases) == 0 {
		s.logf("The bot answers \"no aliases yet\" until the first `relay add`.")
	}
	s.logf("Keep the Mac awake for the runner (`sudo pmset -a sleep 0` or `caffeinate -s`). LaunchAgents start at login: after a reboot, log in once.")
	return nil
}

// Add connects a repository: alias in the bot config, and a self-hosted
// runner for it unless the owner is an organization that already has one.
// Running it again for the same repository is safe.
func (s *Sys) Add(ctx context.Context, alias string, repo gh.Repo) error {
	if _, err := AddAlias(s.configPath(), alias, repo); err != nil {
		return err
	}
	s.logf("alias %s → %s written to %s", strings.ToLower(alias), repo, s.configPath())
	if s.OwnerIsOrg(ctx, repo.Owner) && s.OrgHasRelayRunner(ctx, repo.Owner) {
		s.logf("%s is an organization with a relay runner already; nothing to register", repo.Owner)
		return s.restartBot(ctx)
	}
	tarball, tag, err := s.EnsureDist(ctx)
	if err != nil {
		return err
	}
	s.logf("runner %s cached at %s", tag, tarball)
	if err := s.RegisterRunner(ctx, repo, tarball); err != nil {
		return err
	}
	return s.restartBot(ctx)
}

// restartBot reloads the bot so that it picks up the new alias; a bot that
// is not installed yet is not an error.
func (s *Sys) restartBot(ctx context.Context) error {
	if _, err := os.Stat(s.botPlist()); err != nil {
		s.logf("bot service not installed yet; run `relay setup`")
		return nil
	}
	if err := s.restartAgent(ctx, s.botPlist(), BotLabel); err != nil {
		return err
	}
	s.logf("bot restarted")
	return nil
}

// Doctor runs every machine check and, with a client, the repository
// checks (workflow protocol version, and an Issue when given). It returns
// false when anything failed.
func (s *Sys) Doctor(ctx context.Context, cl gh.Client, issue int) bool {
	checks := []Check{s.CheckClaude(ctx), s.CheckAPIKey(ctx)}
	_, c := s.CheckGH(ctx)
	checks = append(checks, c, s.CheckPlugin(ctx), s.CheckBotService(ctx))
	checks = append(checks, s.CheckRunners(ctx)...)
	if cl != nil {
		checks = append(checks, CheckWorkflow(ctx, cl))
		if issue > 0 {
			checks = append(checks, CheckIssue(ctx, cl, issue)...)
		}
	}
	for _, c := range checks {
		s.logf("%s", c)
	}
	return AllOK(checks)
}
