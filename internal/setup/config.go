package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/gh"
)

// configTemplate renders the bot's config with its comments; durations are
// written as strings, which the YAML loader reads back.
var configTemplate = template.Must(template.New("config").Funcs(template.FuncMap{"q": strconv.Quote}).Parse(`# Relay bot configuration. Written by relay setup / relay add; edit by hand if you like.
# The Telegram token is not here: RELAY_TELEGRAM_TOKEN, the Keychain item, or token_file.

# alias → owner/repo. Send "<alias>: text" to the bot to open an Issue there.
aliases:
{{- range .Aliases}}
  {{.Alias}}: {{q .Repo}}
{{- end}}

# Who may drive the bot: (chat, user) pairs in Telegram's own ids.
allow:
{{- range .Allow}}
  - {chat: "{{.Chat}}", user: "{{.User}}"}
{{- end}}

telegram:
  keychain: {{q .Telegram.Keychain}}
{{- if .Telegram.TokenFile}}
  token_file: {{q .Telegram.TokenFile}}
{{- end}}

poll: {{.Poll}}
lookback: {{.Lookback}}
silence: {{.Silence}}
workflow: {{q .Workflow}}
`))

type aliasRow struct{ Alias, Repo string }

// WriteBotConfig writes the config file, creating its directory.
func WriteBotConfig(path string, c core.Config) error {
	var rows []aliasRow
	for _, a := range c.AliasList() {
		rows = append(rows, aliasRow{a, c.Aliases[a]})
	}
	var buf bytes.Buffer
	err := configTemplate.Execute(&buf, map[string]any{
		"Aliases": rows, "Allow": c.Allow, "Telegram": c.Telegram,
		"Poll": c.Poll.String(), "Lookback": c.Lookback.String(), "Silence": c.Silence.String(), "Workflow": c.Workflow,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// ReadBotConfig loads the file without validating it, or the defaults when
// it does not exist yet.
func ReadBotConfig(path string) (core.Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return core.Defaults(), false, nil
	}
	if err != nil {
		return core.Config{}, false, err
	}
	c, err := core.Decode(data)
	return c, true, err
}

// AddAlias records alias → repo in the config file.
func AddAlias(path, alias string, repo gh.Repo) (core.Config, error) {
	c, _, err := ReadBotConfig(path)
	if err != nil {
		return c, err
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	if !core.ValidAlias(alias) {
		return c, fmt.Errorf("alias %q: use up to %d of letters, digits, - and _", alias, core.MaxAliasLen)
	}
	if c.Aliases == nil {
		c.Aliases = map[string]string{}
	}
	if cur, ok := c.Aliases[alias]; ok && !strings.EqualFold(cur, repo.String()) {
		return c, fmt.Errorf("alias %q already points to %s", alias, cur)
	}
	c.Aliases[alias] = repo.String()
	return c, WriteBotConfig(path, c)
}

// AllowPrincipal records a (chat, user) pair in the config file.
func AllowPrincipal(path, chat, user string) (core.Config, error) {
	c, _, err := ReadBotConfig(path)
	if err != nil {
		return c, err
	}
	if !core.ValidID(chat) || !core.ValidID(user) {
		return c, fmt.Errorf("chat %q and user %q must be numeric ids", chat, user)
	}
	if !c.Allowed(chat, user) {
		c.Allow = append(c.Allow, core.Principal{Chat: chat, User: user})
	}
	return c, WriteBotConfig(path, c)
}
