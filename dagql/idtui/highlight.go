package idtui

import (
	"strings"

	"github.com/muesli/termenv"
)

// searchHighlight defines how a search match is rendered.
type searchHighlight struct {
	bg termenv.Color
	fg termenv.Color
}

var (
	// matchHighlight is used for non-current matches.
	matchHighlight = searchHighlight{
		bg: termenv.ANSIYellow,
		fg: termenv.ANSIBlack,
	}
	// currentMatchHighlight is used for the currently selected match.
	currentMatchHighlight = searchHighlight{
		bg: termenv.ANSIBrightYellow,
		fg: termenv.ANSIBlack,
	}
)

// highlightANSI finds all case-insensitive occurrences of query in the
// visible text of an ANSI-formatted string and wraps them with the given
// highlight style. It preserves existing ANSI sequences.
//
// This works by walking the string, skipping over ESC sequences (which are
// invisible), and matching against the visible characters only.
func highlightANSI(s, query string, style searchHighlight) string {
	if query == "" || s == "" {
		return s
	}

	lowerQuery := strings.ToLower(query)
	queryLen := len(lowerQuery)

	// First pass: build a map from visible-char index to byte offset,
	// and extract the visible text.
	type charInfo struct {
		byteOffset int
		byteLen    int
	}
	var visible []charInfo
	var visibleText strings.Builder

	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip the entire ANSI escape sequence.
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isANSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++ // skip terminator
				}
			} else if j < len(s) && s[j] == ']' {
				// OSC sequence: ESC ] ... ST (or BEL)
				j++
				for j < len(s) && s[j] != '\x07' {
					if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(s) && s[j] == '\x07' {
					j++
				}
			}
			i = j
			continue
		}
		visible = append(visible, charInfo{byteOffset: i, byteLen: 1})
		visibleText.WriteByte(s[i])
		i++
	}

	if len(visible) == 0 {
		return s
	}

	// Find all match positions in visible text.
	lowerVisible := strings.ToLower(visibleText.String())
	type matchRange struct {
		start, end int // indices into visible[]
	}
	var matches []matchRange
	searchFrom := 0
	for {
		idx := strings.Index(lowerVisible[searchFrom:], lowerQuery)
		if idx < 0 {
			break
		}
		matchStart := searchFrom + idx
		matchEnd := matchStart + queryLen
		matches = append(matches, matchRange{matchStart, matchEnd})
		searchFrom = matchEnd
	}

	if len(matches) == 0 {
		return s
	}

	// Build highlight start/end sequences.
	hlStart := termenv.CSI + style.bg.Sequence(true) + ";" + style.fg.Sequence(false) + "m"
	hlEnd := termenv.CSI + termenv.ResetSeq + "m"

	// Build a set of visible-char indices that start/end a highlight.
	hlStarts := make(map[int]bool, len(matches))
	hlEnds := make(map[int]bool, len(matches))
	for _, m := range matches {
		hlStarts[m.start] = true
		hlEnds[m.end] = true
	}

	// Second pass: reconstruct the string, injecting highlight markers.
	var out strings.Builder
	out.Grow(len(s) + len(matches)*(len(hlStart)+len(hlEnd)))

	visIdx := 0
	byteIdx := 0
	inHighlight := false
	// Track the ANSI state so we can restore it after highlight end.
	var lastANSI string

	for byteIdx < len(s) {
		if s[byteIdx] == '\x1b' {
			// Copy the entire escape sequence.
			j := byteIdx + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isANSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			} else if j < len(s) && s[j] == ']' {
				j++
				for j < len(s) && s[j] != '\x07' {
					if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(s) && s[j] == '\x07' {
					j++
				}
			}
			seq := s[byteIdx:j]
			if !inHighlight {
				lastANSI = seq
			}
			out.WriteString(seq)
			byteIdx = j
			continue
		}

		// Visible character.
		if hlEnds[visIdx] && inHighlight {
			out.WriteString(hlEnd)
			if lastANSI != "" {
				out.WriteString(lastANSI)
			}
			inHighlight = false
		}
		if hlStarts[visIdx] && !inHighlight {
			out.WriteString(hlStart)
			inHighlight = true
		}

		out.WriteByte(s[byteIdx])
		visIdx++
		byteIdx++
	}

	if inHighlight {
		out.WriteString(hlEnd)
		if lastANSI != "" {
			out.WriteString(lastANSI)
		}
	}

	return out.String()
}

func isANSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
