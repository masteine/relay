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

{{if .WhatsApp -}}
# Who may drive the bot: (chat, user) pairs in each messenger's own ids;
# rows without "messenger" are Telegram.
{{- else -}}
# Who may drive the bot: (chat, user) pairs in Telegram's own ids.
{{- end}}
allow:
{{- range .Allow}}
{{- if .Messenger}}
  - {messenger: {{.Messenger}}, chat: "{{.Chat}}", user: "{{.User}}"}
{{- else}}
  - {chat: "{{.Chat}}", user: "{{.User}}"}
{{- end}}
{{- end}}

telegram:
  keychain: {{q .Telegram.Keychain}}
{{- if .Telegram.TokenFile}}
  token_file: {{q .Telegram.TokenFile}}
{{- end}}
{{- if .WhatsApp}}

whatsapp:
  store: {{q .WhatsApp.Store}}
{{- end}}

poll: {{.Poll}}
lookback: {{.Lookback}}
silence: {{.Silence}}
workflow: {{q .Workflow}}
`))

type aliasRow struct{ Alias, Repo string }

// WriteBotConfig writes the config file, creating its directory. A
// Telegram-only config gets no messenger or whatsapp keys, so that an older
// binary still reads it.
func WriteBotConfig(path string, c core.Config) error {
	var rows []aliasRow
	for _, a := range c.AliasList() {
		rows = append(rows, aliasRow{a, c.Aliases[a]})
	}
	allow := make([]core.Principal, len(c.Allow))
	for i, p := range c.Allow {
		p.Messenger = core.NormalizeMessenger(p.Messenger)
		if p.Messenger == core.NameTelegram {
			p.Messenger = ""
		}
		allow[i] = p
	}
	var whatsapp *core.WhatsApp
	if c.Has(core.NameWhatsApp) || c.WhatsApp.Store != "" && c.WhatsApp.Store != core.DefaultWhatsAppStore() {
		whatsapp = &c.WhatsApp
		if whatsapp.Store == "" {
			whatsapp = &core.WhatsApp{Store: core.DefaultWhatsAppStore()}
		}
	}
	var buf bytes.Buffer
	err := configTemplate.Execute(&buf, map[string]any{
		"Aliases": rows, "Allow": allow, "Telegram": c.Telegram, "WhatsApp": whatsapp,
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

// AllowPrincipal records a (chat, user) pair of a messenger ("" is
// Telegram) in the config file, once.
func AllowPrincipal(path, messenger, chat, user string) (core.Config, error) {
	c, _, err := ReadBotConfig(path)
	if err != nil {
		return c, err
	}
	p := core.Principal{Messenger: core.NormalizeMessenger(messenger), Chat: chat, User: user}
	if p.Messenger == core.NameTelegram {
		p.Messenger = ""
	}
	if err := p.Validate(); err != nil {
		return c, err
	}
	if !hasPrincipal(c, p) {
		c.Allow = append(c.Allow, p)
	}
	return c, WriteBotConfig(path, c)
}

// hasPrincipal compares normalised messengers, so that a row written as
// "messenger: telegram" and one without the key are the same.
func hasPrincipal(c core.Config, p core.Principal) bool {
	for _, q := range c.Allow {
		if core.NormalizeMessenger(q.Messenger) == core.NormalizeMessenger(p.Messenger) && q.Chat == p.Chat && q.User == p.User {
			return true
		}
	}
	return false
}
