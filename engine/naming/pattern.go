package naming

import "strings"

// MapNamePattern converts the names in a path pattern with convert, leaving
// its syntax alone: convert sees only the runs of name characters (letters,
// digits and the word separators "-_. "), while glob metacharacters,
// [classes], \ escapes and separators such as ':' and '/' stay where they
// are, so a pattern converts like the names it matches. A word separator
// next to syntax is kept, as a '-', so a pattern doesn't broaden: test-*
// stays test-*, not test*.
func MapNamePattern(pattern string, convert func(string) string) string {
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		run := pattern[start:end]
		trimmed := strings.TrimLeft(run, nameSeparators)
		if start > 0 && len(trimmed) < len(run) {
			b.WriteByte('-')
		}
		lead := len(trimmed)
		trimmed = strings.TrimRight(trimmed, nameSeparators)
		if trimmed != "" {
			b.WriteString(convert(trimmed))
		}
		if end < len(pattern) && len(trimmed) < lead && trimmed != "" {
			b.WriteByte('-')
		}
		start = -1
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			flush(i)
			b.WriteString(pattern[i : i+2])
			i++
			continue
		}
		if c == '[' {
			// A glob class is syntax, not a name. In particular, the hyphen
			// in [0-9] must not be consumed as a word separator.
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				if pattern[end] == '\\' && end+1 < len(pattern) {
					end++
				}
				end++
			}
			if end < len(pattern) {
				flush(i)
				b.WriteString(pattern[i : end+1])
				i = end
				continue
			}
		}
		if isNameByte(c) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		b.WriteByte(c)
	}
	flush(len(pattern))
	return b.String()
}

// nameSeparators are the word separators isNameByte accepts.
const nameSeparators = "-_. "

// isNameByte reports whether c can be part of a name in some casing: a
// letter, a digit, or a word separator.
func isNameByte(c byte) bool {
	return isAlnum(c) || strings.IndexByte(nameSeparators, c) >= 0
}
