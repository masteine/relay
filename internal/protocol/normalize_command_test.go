package protocol

import "testing"

func TestNormalizeCommand(t *testing.T) {
	for in, want := range map[string]string{
		"/Approve":              "/approve",
		"\u00a0/Approve":        "\u00a0/approve",
		"/APPROVE":              "/approve",
		"/Retry зараз":          "/retry зараз",
		"  /Ticket Як Є\nДалі":  "  /ticket Як Є\nДалі",
		"\n\n/Approve\n":        "\n\n/approve\n",
		"/approve":              "/approve",
		"/Usr/bin":              "/Usr/bin",
		"/Approve/x":            "/Approve/x",
		"/Approvex":             "/Approvex",
		"Текст\n/Approve":       "Текст\n/Approve",
		"Що з /Approve?":        "Що з /Approve?",
		"":                      "",
		"   ":                   "   ",
		"/Reject Не Той Модуль": "/reject Не Той Модуль",
	} {
		got := NormalizeCommand(in)
		if got != want {
			t.Errorf("NormalizeCommand(%q) = %q, want %q", in, got, want)
		}
		_, before := ParseCommand(in)
		if _, after := ParseCommand(got); before && !after {
			t.Errorf("%q: a command stopped parsing", in)
		}
	}
	for _, in := range []string{"/Approve", "/APPROVE", "/Retry зараз"} {
		if _, ok := ParseCommand(NormalizeCommand(in)); !ok {
			t.Errorf("%q does not parse after normalising", in)
		}
	}
}
