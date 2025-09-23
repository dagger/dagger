package gitutil

import (
	"path"
	"unicode"
	"unicode/utf8"
)

var ErrBadPattern = path.ErrBadPattern

// gitTailMatch implements the same semantics as `git ls-remote` patterns,
// matching from the end of the name.
//
// This is what allows `main` to match `refs/heads/main`, etc.
func gitTailMatch(pattern, name string) (matched bool, err error) {
	return gitMatch("*/"+pattern, "/"+name)
}

// gitMatch is an adaptation of [path.Match] relaxed to mimic git's wildmatch
// behavior in https://github.com/git/git/blob/main/wildmatch.c (without `WM_PATHNAME`).
//
// There are a few major differences from [path.Match]:
// - `**` can be used in place of `*`
// - `*` and `?` match `/`
// - both `!` and `^` can be used to negate character classes
// - posix character classes like `[:alnum:]` are supported
func gitMatch(pattern, name string) (matched bool, err error) {
Pattern:
	for len(pattern) > 0 {
		var star bool
		var chunk string
		star, chunk, pattern = scanChunk(pattern)
		if star && chunk == "" {
			return true, nil
		}
		// Look for match at current position.
		t, ok, err := matchChunk(chunk, name)
		// if we're the last chunk, make sure we've exhausted the name
		// otherwise we'll give a false result even if we could still match
		// using the star
		if ok && (len(t) == 0 || len(pattern) > 0) {
			name = t
			continue
		}
		if err != nil {
			return false, err
		}
		if star {
			// Look for match skipping i+1 bytes.
			for i := 0; i < len(name); i++ {
				t, ok, err := matchChunk(chunk, name[i+1:])
				if ok {
					// if we're the last chunk, make sure we exhausted the name
					if len(pattern) == 0 && len(t) > 0 {
						continue
					}
					name = t
					continue Pattern
				}
				if err != nil {
					return false, err
				}
			}
		}
		return false, nil
	}
	return len(name) == 0, nil
}

// scanChunk gets the next segment of pattern, which is a non-star string
// possibly preceded by a star.
func scanChunk(pattern string) (star bool, chunk, rest string) {
	for len(pattern) > 0 && pattern[0] == '*' {
		pattern = pattern[1:]
		star = true
	}
	inrange := false
	var i int
Scan:
	for i = 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			// error check handled in matchChunk: bad pattern.
			if i+1 < len(pattern) {
				i++
			}
		case '[':
			inrange = true
		case ']':
			inrange = false
		case '*':
			if !inrange {
				break Scan
			}
		}
	}
	return star, pattern[0:i], pattern[i:]
}

// matchChunk checks whether chunk matches the beginning of s.
// If so, it returns the remainder of s (after the match).
// Chunk is all single-character operators: literals, char classes, and ?.
//
//nolint:gocyclo
func matchChunk(chunk, s string) (rest string, ok bool, err error) {
	// failed records whether the match has failed.
	// After the match fails, the loop continues on processing chunk,
	// checking that the pattern is well-formed but no longer reading s.
	failed := false
	for len(chunk) > 0 {
		if !failed && len(s) == 0 {
			failed = true
		}
		switch chunk[0] {
		case '[':
			// character class
			var r rune
			if !failed {
				var n int
				r, n = utf8.DecodeRuneInString(s)
				s = s[n:]
			}
			chunk = chunk[1:]
			// possibly negated
			negated := false
			if len(chunk) > 0 && (chunk[0] == '^' || chunk[0] == '!') {
				negated = true
				chunk = chunk[1:]
			}
			// handle character class types
			match := false
			nrange := 0
			if len(chunk) > 1 && chunk[0] == '[' && chunk[1] == ':' {
				// look for closing ]
				for i := 2; i < len(chunk); i++ {
					if chunk[i] == ']' && chunk[i-1] == ':' {
						class := chunk[2 : i-1]
						match = matchClass(r, class)
						chunk = chunk[i+1:]
						goto check
					}
				}
			}
			// parse all ranges
			for {
				if len(chunk) > 0 && chunk[0] == ']' && nrange > 0 {
					break
				}
				var lo, hi rune
				if lo, chunk, err = getEsc(chunk); err != nil {
					return "", false, err
				}
				hi = lo
				if chunk[0] == '-' {
					if hi, chunk, err = getEsc(chunk[1:]); err != nil {
						return "", false, err
					}
				}
				if lo <= r && r <= hi {
					match = true
				}
				nrange++
			}
		check:
			chunk = chunk[1:]
			if match == negated {
				failed = true
			}

		case '?':
			if !failed {
				_, n := utf8.DecodeRuneInString(s)
				s = s[n:]
			}
			chunk = chunk[1:]

		case '\\':
			chunk = chunk[1:]
			if len(chunk) == 0 {
				return "", false, ErrBadPattern
			}
			fallthrough

		default:
			if !failed {
				if chunk[0] != s[0] {
					failed = true
				}
				s = s[1:]
			}
			chunk = chunk[1:]
		}
	}
	if failed {
		return "", false, nil
	}
	return s, true, nil
}

// getEsc gets a possibly-escaped character from chunk, for a character class.
func getEsc(chunk string) (r rune, nchunk string, err error) {
	if len(chunk) == 0 || chunk[0] == '-' || chunk[0] == ']' {
		err = ErrBadPattern
		return
	}
	if chunk[0] == '\\' {
		chunk = chunk[1:]
		if len(chunk) == 0 {
			err = ErrBadPattern
			return
		}
	}
	r, n := utf8.DecodeRuneInString(chunk)
	if r == utf8.RuneError && n == 1 {
		err = ErrBadPattern
	}
	nchunk = chunk[n:]
	if len(nchunk) == 0 {
		err = ErrBadPattern
	}
	return
}

// matchClass checks whether r is in the given character class
func matchClass(r rune, class string) bool {
	switch class {
	case "alnum":
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	case "alpha":
		return unicode.IsLetter(r)
	case "blank":
		return r == ' ' || r == '\t'
	case "cntrl":
		return unicode.IsControl(r)
	case "digit":
		return unicode.IsDigit(r)
	case "graph":
		return unicode.IsGraphic(r)
	case "lower":
		return unicode.IsLower(r)
	case "print":
		return unicode.IsPrint(r)
	case "punct":
		return unicode.IsPunct(r)
	case "space":
		return unicode.IsSpace(r)
	case "upper":
		return unicode.IsUpper(r)
	case "xdigit":
		return unicode.Is(unicode.Hex_Digit, r)
	default:
		// malformed [:class:]
		return false
	}
}
