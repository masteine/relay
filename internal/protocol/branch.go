package protocol

import (
	"fmt"
	"strings"
	"unicode"
)

// SlugMaxLen bounds the slug part of branch and spec names.
const SlugMaxLen = 40

// BranchName is the work branch of an Issue: relay/<n>-<slug>.
func BranchName(issue int, title string) string {
	return fmt.Sprintf("relay/%d-%s", issue, Slug(title))
}

// SpecPath is where the plan stage writes the spec: docs/specs/<n>-<slug>.md.
func SpecPath(issue int, title string) string {
	return fmt.Sprintf("docs/specs/%d-%s.md", issue, Slug(title))
}

// Slug turns a title into up to SlugMaxLen characters of [a-z0-9-].
// Cyrillic is transliterated the way Ukrainian passports do it, so that a
// Ukrainian title still gives a readable branch name. An empty result
// becomes "task".
func Slug(title string) string {
	var b strings.Builder
	rs := []rune(strings.ToLower(title))
	prevLetter := false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 'з' && i+1 < len(rs) && rs[i+1] == 'г' {
			// "зг" is "zgh" so that it does not read as "ж".
			b.WriteString("zgh")
			prevLetter = true
			i++
			continue
		}
		if t, ok := translit[r]; ok {
			if !prevLetter {
				if s, ok := translitStart[r]; ok {
					t = s
				}
			}
			b.WriteString(t)
			prevLetter = true
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevLetter = true
		case r == '\'' || r == '’' || r == 'ʼ' || r == 'ь' || r == 'ъ':
			// Soft sign and apostrophes: silent, and they do not break a word.
		default:
			b.WriteRune('-')
			prevLetter = unicode.IsLetter(r)
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > SlugMaxLen {
		s = strings.TrimRight(s[:SlugMaxLen], "-")
	}
	if s == "" {
		return "task"
	}
	return s
}

// translit follows the Ukrainian national transliteration (2010), plus the
// few Russian letters that show up in mixed text.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "h", 'ґ': "g", 'д': "d", 'е': "e",
	'є': "ie", 'ж': "zh", 'з': "z", 'и': "y", 'і': "i", 'ї': "i", 'й': "i",
	'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch",
	'ш': "sh", 'щ': "shch", 'ю': "iu", 'я': "ia",
	'ы': "y", 'э': "e", 'ё': "io",
}

// translitStart overrides translit at the start of a word.
var translitStart = map[rune]string{
	'є': "ye", 'ї': "yi", 'й': "y", 'ю': "yu", 'я': "ya", 'ё': "yo",
}
