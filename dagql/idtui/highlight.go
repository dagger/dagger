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
	// matchHighlight is used for non-current matches (white bg).
	matchHighlight = searchHighlight{
		bg: termenv.ANSIWhite,
		fg: termenv.ANSIBlack,
	}
	// currentMatchHighlight is used for the currently selected match (yellow bg).
	currentMatchHighlight = searchHighlight{
		bg: termenv.ANSIYellow,
		fg: termenv.ANSIBlack,
	}
)

// highlightANSI finds all case-insensitive occurrences of query in the
// visible text of an ANSI-formatted string and wraps them with the given
// highlight style. It preserves existing ANSI sequences.
//
// The key challenge is that ANSI formatting is cumulative across multiple
// CSI sequences (e.g., faint + foreground color). We can't just track the
// "last" sequence — we need to replay ALL sequences seen before the highlight
// to fully restore the prior formatting state after the highlight ends.
func highlightANSI(s, query string, style searchHighlight) string {
	if query == "" || s == "" {
		return s
	}

	lowerQuery := strings.ToLower(query)
	queryLen := len(lowerQuery)

	// First pass: extract visible text (skipping ANSI sequences) and record
	// byte offsets so we can map match positions back to the original string.
	var visibleText strings.Builder

	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			i = skipANSI(s, i)
			continue
		}
		visibleText.WriteByte(s[i])
		i++
	}

	if visibleText.Len() == 0 {
		return s
	}

	// Find all match positions in visible text (byte indices).
	lowerVisible := strings.ToLower(visibleText.String())
	type matchRange struct {
		start, end int // byte indices into visible text
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

	// Build highlight start sequence.
	hlStart := termenv.CSI + style.bg.Sequence(true) + ";" + style.fg.Sequence(false) + "m"
	hlEnd := termenv.CSI + termenv.ResetSeq + "m" // full reset

	// Build sets of visible-byte indices that start/end a highlight.
	hlStarts := make(map[int]bool, len(matches))
	hlEnds := make(map[int]bool, len(matches))
	for _, m := range matches {
		hlStarts[m.start] = true
		hlEnds[m.end] = true
	}

	// Second pass: reconstruct the string, injecting highlight markers.
	// We accumulate ALL ANSI sequences seen outside of highlights so that
	// after hlEnd (full reset) we can replay them to restore the complete
	// formatting state (faint, bold, fg color, etc.).
	var out strings.Builder
	out.Grow(len(s) + len(matches)*(len(hlStart)+len(hlEnd)+32))

	visIdx := 0  // index into visible text (bytes)
	byteIdx := 0 // index into original string s
	inHighlight := false

	// savedANSI accumulates all ANSI sequences seen while NOT in a highlight.
	// When a highlight ends, we replay them all to restore cumulative state.
	var savedANSI strings.Builder

	for byteIdx < len(s) {
		if s[byteIdx] == '\x1b' {
			// Copy the entire escape sequence to output.
			j := skipANSI(s, byteIdx)
			seq := s[byteIdx:j]
			if !inHighlight {
				savedANSI.WriteString(seq)
			}
			out.WriteString(seq)
			byteIdx = j
			continue
		}

		// Visible character.
		if hlEnds[visIdx] && inHighlight {
			// End highlight: full reset then replay all prior ANSI state.
			out.WriteString(hlEnd)
			out.WriteString(savedANSI.String())
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
		out.WriteString(savedANSI.String())
	}

	return out.String()
}

// skipANSI returns the byte index past the ANSI escape sequence starting at
// position i in s. Handles CSI (ESC [) and OSC (ESC ]) sequences.
func skipANSI(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[': // CSI sequence
		j++
		for j < len(s) && !isANSITerminator(s[j]) {
			j++
		}
		if j < len(s) {
			j++ // skip terminator
		}
	case ']': // OSC sequence: ESC ] ... ST (or BEL)
		j++
		for j < len(s) && s[j] != '\x07' {
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				j += 2
				return j
			}
			j++
		}
		if j < len(s) && s[j] == '\x07' {
			j++
		}
	default:
		// Unknown escape — skip just the ESC byte; next byte is not consumed.
	}
	return j
}

func isANSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
