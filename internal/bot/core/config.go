package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// DefaultWhatsAppStore is where the WhatsApp session is kept.
func DefaultWhatsAppStore() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "relay", "whatsapp", "session.db")
}

// Messenger is one messenger the bot can talk through.
type Messenger struct {
	// Name is how the config names it ("messenger: whatsapp").
	Name string
	// Key is its key in the transport.Mux and, unless Unprefixed, the
	// prefix of its chat ids in the core ("wa:380…").
	Key        string
	Unprefixed bool
	// Display is the name humans read in notices.
	Display string
	// Setup is the command that connects it again.
	Setup string
}

// Messenger names in the config.
const (
	NameTelegram = "telegram"
	NameWhatsApp = "whatsapp"
)

// Messengers is the one table of messengers: config names, Mux keys and
// chat id prefixes all come from here. Telegram is unprefixed, so its ids
// are what they were before WhatsApp.
var Messengers = []Messenger{
	{Name: NameTelegram, Key: "tg", Unprefixed: true, Display: "Telegram", Setup: "relay setup"},
	{Name: NameWhatsApp, Key: "wa", Display: "WhatsApp", Setup: "relay setup --whatsapp"},
}

// NormalizeMessenger is the messenger name every comparison uses: an empty
// one (every row written before WhatsApp) is Telegram.
func NormalizeMessenger(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return NameTelegram
	}
	return name
}

// MessengerNamed finds a messenger by its config name.
func MessengerNamed(name string) (Messenger, bool) {
	name = NormalizeMessenger(name)
	for _, m := range Messengers {
		if m.Name == name {
			return m, true
		}
	}
	return Messenger{}, false
}

// MessengerKeyed finds a messenger by its Mux key.
func MessengerKeyed(key string) (Messenger, bool) {
	for _, m := range Messengers {
		if m.Key == key {
			return m, true
		}
	}
	return Messenger{}, false
}

// UnprefixedKey is the Mux key whose chat ids carry no prefix.
func UnprefixedKey() string {
	for _, m := range Messengers {
		if m.Unprefixed {
			return m.Key
		}
	}
	return ""
}

// Principal is one allowed (chat, user) pair, in the messenger's own ids.
// In a group chat only the listed user may drive the bot. Messenger is
// empty for Telegram, so that a Telegram-only file has no new keys.
type Principal struct {
	Messenger string `yaml:"messenger,omitempty"`
	Chat      string `yaml:"chat"`
	User      string `yaml:"user"`
}

// ChatKey is the chat as the core and the transport.Mux see it: raw for
// Telegram, "wa:"+Chat for WhatsApp.
func (p Principal) ChatKey() string {
	m, ok := MessengerNamed(p.Messenger)
	if !ok {
		return NormalizeMessenger(p.Messenger) + ":" + p.Chat // routes nowhere
	}
	if m.Unprefixed {
		return p.Chat
	}
	return m.Key + ":" + p.Chat
}

// Validate checks the ids by the messenger's rules: numeric ids for
// Telegram; for WhatsApp a private chat by phone number, chat == user.
func (p Principal) Validate() error {
	switch NormalizeMessenger(p.Messenger) {
	case NameTelegram:
		if !ValidID(p.Chat) || !ValidID(p.User) {
			return fmt.Errorf("chat and user must be numeric ids, got chat %q user %q", p.Chat, p.User)
		}
	case NameWhatsApp:
		if !ValidJID(p.Chat) || p.User != p.Chat {
			return fmt.Errorf("whatsapp: chat and user must be the same <phone>@s.whatsapp.net (private chats only), got chat %q user %q", p.Chat, p.User)
		}
	default:
		return fmt.Errorf("unknown messenger %q", p.Messenger)
	}
	return nil
}

var jidRe = regexp.MustCompile(`^[0-9]{5,20}@s\.whatsapp\.net$`)

// ValidJID accepts a WhatsApp user by phone number. Groups (@g.us) and
// hidden ids (@lid) are refused: the bot answers private chats only.
func ValidJID(s string) bool { return jidRe.MatchString(s) }

// Telegram says where the bot token comes from. The token itself is never
// in this file: RELAY_TELEGRAM_TOKEN in the environment, then the Keychain
// item, then the file.
type Telegram struct {
	Keychain  string `yaml:"keychain"`   // Keychain service name, default "relay-telegram"
	TokenFile string `yaml:"token_file"` // fallback file with the token, mode 0600
}

// WhatsApp says where the WhatsApp session is kept.
type WhatsApp struct {
	Store string `yaml:"store"`
}

// Config is ~/.config/relay/config.yml.
type Config struct {
	// Aliases map a short name to owner/repo. Aliases are case-insensitive
	// and stored lowercase.
	Aliases  map[string]string `yaml:"aliases"`
	Allow    []Principal       `yaml:"allow"`
	Telegram Telegram          `yaml:"telegram"`
	WhatsApp WhatsApp          `yaml:"whatsapp"`
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
		WhatsApp: WhatsApp{Store: DefaultWhatsAppStore()},
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
	if in.WhatsApp.Store != "" {
		c.WhatsApp.Store = in.WhatsApp.Store
	}
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
		if err := p.Validate(); err != nil {
			return fmt.Errorf("allow: %w", err)
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

// Allowed reports whether the (chat, user) pair may drive the bot; chat is
// a ChatKey.
func (c Config) Allowed(chat, user string) bool {
	for _, p := range c.Allow {
		if p.ChatKey() == chat && p.User == user {
			return true
		}
	}
	return false
}

// Chats lists the allowed chats as ChatKeys, each once, in a stable order.
// Forwarded comments go to all of them.
func (c Config) Chats() []string { return c.chats(func(Principal) bool { return true }) }

// ChatsExcept lists the allowed chats of every messenger but one, for a
// notice that this one is down.
func (c Config) ChatsExcept(messenger string) []string {
	name := NormalizeMessenger(messenger)
	return c.chats(func(p Principal) bool { return NormalizeMessenger(p.Messenger) != name })
}

// Has reports whether any allowed chat is on the messenger.
func (c Config) Has(messenger string) bool {
	name := NormalizeMessenger(messenger)
	for _, p := range c.Allow {
		if NormalizeMessenger(p.Messenger) == name {
			return true
		}
	}
	return false
}

func (c Config) chats(keep func(Principal) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range c.Allow {
		if k := p.ChatKey(); keep(p) && !seen[k] {
			seen[k] = true
			out = append(out, k)
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
