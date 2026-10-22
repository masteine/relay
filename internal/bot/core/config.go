package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/masteine/relay/internal/gh"
)

// DefaultConfigPath is where `relay setup` writes the bot's configuration.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "relay", "config.yml")
}

// MaxAliasLen keeps button data ("t:<alias>:<n>") inside Telegram's 64
// bytes with room for a large Issue number.
const MaxAliasLen = 32

// Principal is one allowed (chat, user) pair, in the messenger's own ids.
// In a group chat only the listed user may drive the bot.
type Principal struct {
	Chat string `yaml:"chat"`
	User string `yaml:"user"`
}

// Telegram says where the bot token comes from. The token itself is never
// in this file: RELAY_TELEGRAM_TOKEN in the environment, then the Keychain
// item, then the file.
type Telegram struct {
	Keychain  string `yaml:"keychain"`   // Keychain service name, default "relay-telegram"
	TokenFile string `yaml:"token_file"` // fallback file with the token, mode 0600
}

// Config is ~/.config/relay/config.yml.
type Config struct {
	// Aliases map a short name to owner/repo. Aliases are case-insensitive
	// and stored lowercase.
	Aliases  map[string]string `yaml:"aliases"`
	Allow    []Principal       `yaml:"allow"`
	Telegram Telegram          `yaml:"telegram"`
	// Poll is how often the bot looks at GitHub.
	Poll time.Duration `yaml:"poll"`
	// Lookback is how far back the first poll after a start looks.
	Lookback time.Duration `yaml:"lookback"`
	// Silence is how long after a command or reply the bot waits for a run
	// or a Relay comment before saying the runner seems down.
	Silence time.Duration `yaml:"silence"`
	// Workflow is the workflow file the silence check looks at.
	Workflow string `yaml:"workflow"`
}

// Defaults are applied to missing fields.
func Defaults() Config {
	return Config{
		Aliases:  map[string]string{},
		Telegram: Telegram{Keychain: "relay-telegram"},
		Poll:     20 * time.Second,
		Lookback: 24 * time.Hour,
		Silence:  10 * time.Minute,
		Workflow: "relay.yml",
	}
}

// LoadConfig reads and validates the file at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data)
}

// ParseConfig reads the configuration from memory and validates it.
func ParseConfig(data []byte) (Config, error) {
	c, err := Decode(data)
	if err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("bot config: %w", err)
	}
	return c, nil
}

// Decode reads the configuration without validating it, for `relay setup`
// and `relay add`, which build the file up step by step.
func Decode(data []byte) (Config, error) {
	c := Defaults()
	var in Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("bot config: %w", err)
	}
	c.Aliases = map[string]string{}
	for alias, repo := range in.Aliases {
		low := strings.ToLower(alias)
		if _, dup := c.Aliases[low]; dup {
			return Config{}, fmt.Errorf("bot config: aliases %q and another differ only by case", alias)
		}
		c.Aliases[low] = repo
	}
	c.Allow = in.Allow
	if in.Telegram.Keychain != "" {
		c.Telegram.Keychain = in.Telegram.Keychain
	}
	c.Telegram.TokenFile = in.Telegram.TokenFile
	if in.Poll != 0 {
		c.Poll = in.Poll
	}
	if in.Lookback != 0 {
		c.Lookback = in.Lookback
	}
	if in.Silence != 0 {
		c.Silence = in.Silence
	}
	if in.Workflow != "" {
		c.Workflow = in.Workflow
	}
	return c, nil
}

// Validate checks what the core relies on.
func (c Config) Validate() error {
	// Zero aliases is allowed: right after `relay setup` the bot runs and
	// answers "no aliases yet" until the first `relay add`.
	for alias, repo := range c.Aliases {
		if !ValidAlias(alias) {
			return fmt.Errorf("alias %q: use up to %d of letters, digits, - and _", alias, MaxAliasLen)
		}
		if _, err := gh.ParseRepo(repo); err != nil {
			return fmt.Errorf("alias %q: %w", alias, err)
		}
	}
	if len(c.Allow) == 0 {
		return fmt.Errorf("no allowed chats: the bot would answer nobody")
	}
	for _, p := range c.Allow {
		if !ValidID(p.Chat) || !ValidID(p.User) {
			return fmt.Errorf("allow: chat and user must be numeric ids, got chat %q user %q", p.Chat, p.User)
		}
	}
	if c.Poll < 5*time.Second {
		return fmt.Errorf("poll %s is below 5s", c.Poll)
	}
	return nil
}

// ValidID accepts a messenger id: an optionally negative integer (Telegram
// group ids are negative).
func ValidID(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		if r == '-' && i == 0 && len(s) > 1 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ValidAlias accepts a lowercase alias of letters, digits, - and _.
func ValidAlias(s string) bool {
	if s == "" || len(s) > MaxAliasLen {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Allowed reports whether the (chat, user) pair may drive the bot.
func (c Config) Allowed(chat, user string) bool {
	for _, p := range c.Allow {
		if p.Chat == chat && p.User == user {
			return true
		}
	}
	return false
}

// Chats lists the allowed chats, each once, in a stable order. Forwarded
// comments go to all of them.
func (c Config) Chats() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range c.Allow {
		if !seen[p.Chat] {
			seen[p.Chat] = true
			out = append(out, p.Chat)
		}
	}
	sort.Strings(out)
	return out
}

// Repo resolves an alias, whatever its case.
func (c Config) Repo(alias string) (gh.Repo, bool) {
	full, ok := c.Aliases[strings.ToLower(alias)]
	if !ok {
		return gh.Repo{}, false
	}
	r, err := gh.ParseRepo(full)
	return r, err == nil
}

// Alias finds the alias of a repository; with several, the first in sorted
// order.
func (c Config) Alias(repo gh.Repo) (string, bool) {
	for _, alias := range c.AliasList() {
		if strings.EqualFold(c.Aliases[alias], repo.String()) {
			return alias, true
		}
	}
	return "", false
}

// AliasList renders the aliases for a hint, sorted.
func (c Config) AliasList() []string {
	out := make([]string, 0, len(c.Aliases))
	for a := range c.Aliases {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
