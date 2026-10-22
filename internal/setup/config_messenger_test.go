package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/masteine/relay/internal/bot/core"
	"github.com/masteine/relay/internal/gh"
)

const waJID = "380501234567@s.whatsapp.net"

// TestConfigV0TelegramUnchanged: a file written before WhatsApp loads, and
// writing it back gives the same bytes, so that an older binary still reads
// what a newer one wrote.
func TestConfigV0TelegramUnchanged(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "config", "v0-telegram.yml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.ParseConfig(want)
	if err != nil {
		t.Fatalf("v0 config must load: %v", err)
	}
	if !c.Has(core.NameTelegram) || c.Has(core.NameWhatsApp) || c.WhatsApp.Store != core.DefaultWhatsAppStore() {
		t.Errorf("config = %+v", c)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := WriteBotConfig(path, c); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(want) {
		t.Errorf("rewritten v0 config differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Adding a Telegram chat keeps the file free of new keys.
	if _, err := AllowPrincipal(path, "", "-100999", "8"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if strings.Contains(string(got), "messenger") || strings.Contains(string(got), "whatsapp") {
		t.Errorf("Telegram-only file has new keys:\n%s", got)
	}
}

func TestConfigWhatsAppRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	c := core.Defaults()
	c.Aliases = map[string]string{"todo": "masteine/relay-sandbox"}
	c.Allow = []core.Principal{{Chat: "-100123", User: "7"}, {Messenger: core.NameWhatsApp, Chat: waJID, User: waJID}}
	c.WhatsApp.Store = "/Users/me/.config/relay/whatsapp/session.db"
	if err := WriteBotConfig(path, c); err != nil {
		t.Fatal(err)
	}
	if _, err := AddAlias(path, "web", gh.Repo{Owner: "masteine", Name: "site"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowPrincipal(path, core.NameTelegram, "-100999", "8"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{
		`  - {chat: "-100123", user: "7"}`,
		`  - {messenger: whatsapp, chat: "` + waJID + `", user: "` + waJID + `"}`,
		`  - {chat: "-100999", user: "8"}`,
		"whatsapp:\n  store: \"/Users/me/.config/relay/whatsapp/session.db\"\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "messenger: telegram") {
		t.Errorf("telegram written explicitly:\n%s", text)
	}
	back, err := core.LoadConfig(path)
	if err != nil {
		t.Fatalf("must load and validate: %v", err)
	}
	if len(back.Allow) != 3 || back.Aliases["web"] != "masteine/site" || back.WhatsApp.Store != c.WhatsApp.Store || !back.Allowed("wa:"+waJID, waJID) {
		t.Errorf("config = %+v", back)
	}
	// Decode(write(c)) == c.
	again := filepath.Join(t.TempDir(), "again.yml")
	if err := WriteBotConfig(again, back); err != nil {
		t.Fatal(err)
	}
	second, _, err := ReadBotConfig(again)
	if err != nil || !reflect.DeepEqual(second, back) {
		t.Errorf("round trip:\n%+v\n%+v (%v)", second, back, err)
	}
	if data2, _ := os.ReadFile(again); string(data2) != text {
		t.Errorf("second write differs:\n%s", data2)
	}
}

func TestConfigAllowPrincipalDedupesMessengers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("aliases: {todo: masteine/relay-sandbox}\nallow:\n  - {messenger: telegram, chat: \"-100123\", user: \"7\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"", "telegram", "Telegram"} {
		if _, err := AllowPrincipal(path, m, "-100123", "7"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := AllowPrincipal(path, core.NameWhatsApp, waJID, waJID); err != nil {
			t.Fatal(err)
		}
	}
	c, _, err := ReadBotConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Allow) != 2 {
		t.Errorf("allow = %+v", c.Allow)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "messenger: telegram") || strings.Count(string(data), "messenger: whatsapp") != 1 || !strings.Contains(string(data), "\nwhatsapp:\n") {
		t.Errorf("file:\n%s", data)
	}
	for _, bad := range [][3]string{
		{core.NameWhatsApp, "120363025@g.us", "120363025@g.us"},
		{core.NameWhatsApp, "12345678901234@lid", "12345678901234@lid"},
		{core.NameWhatsApp, waJID, "380509999999@s.whatsapp.net"},
		{core.NameWhatsApp, "-100123", "7"},
		{"signal", "1", "1"},
		{core.NameTelegram, waJID, waJID},
	} {
		if _, err := AllowPrincipal(path, bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestConfigWhatsAppBlockOnlyWhenNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	c := core.Defaults()
	c.Allow = []core.Principal{{Chat: "1", User: "2"}}
	c.WhatsApp.Store = "/elsewhere/session.db"
	if err := WriteBotConfig(path, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "whatsapp:\n  store: \"/elsewhere/session.db\"") {
		t.Errorf("a custom store must be kept:\n%s", data)
	}
	c.WhatsApp = core.WhatsApp{}
	if err := WriteBotConfig(path, c); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "whatsapp") {
		t.Errorf("no block without a WhatsApp chat or custom store:\n%s", data)
	}
}
