package protocol

import "testing"

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Add dark theme", "add-dark-theme"},
		{"  Fix: crash on launch (Android 14)!  ", "fix-crash-on-launch-android-14"},
		{"Додати темну тему", "dodaty-temnu-temu"},
		{"Їжак їсть яблуко", "yizhak-yist-yabluko"},
		{"Зв'язок із сервером", "zviazok-iz-serverom"},
		{"Звʼязок (U+02BC) і згода", "zviazok-u-02bc-i-zghoda"},
		{"Розгін", "rozghin"},
		{"Щось пішло не так", "shchos-pishlo-ne-tak"},
		{"Юзер → юзер", "yuzer-yuzer"},
		{"Ёлка", "yolka"},
		{"version 2.0 / beta", "version-2-0-beta"},
		{"", "task"},
		{"!!! ???", "task"},
		{"a very long title that goes well past the forty character limit", "a-very-long-title-that-goes-well-past-th"},
		// 39 characters, then a dash exactly at the cut: the dash is trimmed.
		{"abcdefghij-abcdefghij-abcdefghij-abcdef-xyz", "abcdefghij-abcdefghij-abcdefghij-abcdef"},
	}
	for _, tc := range tests {
		if got := Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if l := len(Slug(tc.in)); l > SlugMaxLen {
			t.Errorf("Slug(%q) is %d chars", tc.in, l)
		}
	}
}

func TestBranchAndSpec(t *testing.T) {
	if got := BranchName(42, "Додати темну тему"); got != "relay/42-dodaty-temnu-temu" {
		t.Errorf("BranchName = %q", got)
	}
	if got := SpecPath(42, "Додати темну тему"); got != "docs/specs/42-dodaty-temnu-temu.md" {
		t.Errorf("SpecPath = %q", got)
	}
}
