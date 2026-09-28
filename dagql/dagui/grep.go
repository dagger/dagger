package dagui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

const (
	grepCallStringLimit  = 200 // displayed characters, not bytes
	grepCallLineBytes    = 4096
	grepCallsBytes       = 32 * 1024
	grepCallsFooterBytes = 256
)

// GrepCalls searches the full rendered content of every ingested call, including
// ordinary string arguments. Digested strings remain opaque. Results are sorted
// by digest, with long strings abbreviated around the first match when possible.
// Per-line and total byte budgets also bound large collections of arguments.
// max limits the number of calls (0 = no count limit, still byte-bounded).
func (db *DB) GrepCalls(re *regexp.Regexp, max int) []string {
	digests := make([]string, 0, len(db.Calls))
	for dig := range db.Calls {
		digests = append(digests, dig)
	}
	sort.Strings(digests)
	var lines []string
	var matches, size int
	var capped bool
	for _, dig := range digests {
		frame := db.Call(dig)
		if frame == nil {
			continue
		}
		line := renderCallLine(frame)
		match := re.FindStringIndex(line.text)
		if match == nil {
			continue
		}
		matches++
		if capped || (max > 0 && len(lines) >= max) {
			continue
		}
		preview := line.preview(match)
		if size+len(preview)+1 > grepCallsBytes-grepCallsFooterBytes {
			capped = true
			continue
		}
		lines = append(lines, preview)
		size += len(preview) + 1
	}
	if omitted := matches - len(lines); omitted > 0 {
		reason := "raise the cap or tighten the pattern"
		if capped {
			reason = "response size limit; tighten the pattern"
		}
		lines = append(lines, fmt.Sprintf("… %d more (%s)", omitted, reason))
	}
	return lines
}

type callTextRange struct{ start, end int }

type callSearchLine struct {
	text    string
	strings []callTextRange
	args    callTextRange
}

// preview retains call and receiver digests and abbreviates only string literals
// in ordinary calls. Pathological argument lists get a bounded excerpt instead.
func (line callSearchLine) preview(match []int) string {
	var b strings.Builder
	pos := 0
	for _, part := range line.strings {
		b.WriteString(line.text[pos:part.start])
		b.WriteString(callTextExcerpt(line.text[part.start:part.end], match[0]-part.start, match[1]-part.start, grepCallStringLimit))
		pos = part.end
	}
	b.WriteString(line.text[pos:])
	preview := b.String()
	if len(preview) <= grepCallLineBytes {
		return preview
	}
	// Bound collections as well as individual strings. Use the same excerpt
	// length so this fallback cannot reveal a longer string than the normal
	// preview. Original offsets still locate the match after string elision.
	preview = line.text[:line.args.start] + callTextExcerpt(line.text[line.args.start:line.args.end], match[0]-line.args.start, match[1]-line.args.start, grepCallStringLimit) + line.text[line.args.end:]
	if len(preview) > grepCallLineBytes {
		// Field/type names and non-string literals can be pathological too.
		preview = callTextExcerpt(preview, 0, 0, 800)
	}
	return preview
}

// callTextExcerpt operates on rendered text, not the search input. The match
// coordinates are byte offsets relative to s; matches outside s keep its prefix.
// Explicit character counts distinguish omitted text from literal ellipses.
func callTextExcerpt(s string, matchStart, matchEnd, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	start := 0
	if matchEnd > 0 && matchStart < len(s) {
		at := utf8.RuneCountInString(s[:max(0, matchStart)])
		end := utf8.RuneCountInString(s[:min(len(s), matchEnd)])
		if end > limit {
			start = min(max(0, at-limit/4), len(runes)-limit)
		}
	}
	end := start + limit
	var b strings.Builder
	if start > 0 {
		fmt.Fprintf(&b, "…(%d chars omitted)", start)
	}
	b.WriteString(string(runes[start:end]))
	if end < len(runes) {
		fmt.Fprintf(&b, "…(%d chars omitted)", len(runes)-end)
	}
	return b.String()
}

// renderCallLine keeps the full search text plus the positions of string
// literals so display truncation never changes regexp matching semantics.
// Receiver and ID-argument digests are printed verbatim for chain navigation.
func renderCallLine(frame *callpbv1.Call) callSearchLine {
	var b strings.Builder
	var line callSearchLine
	b.WriteString(frame.GetDigest())
	b.WriteString("  ")
	b.WriteString(frame.GetField())
	b.WriteString("(")
	line.args.start = b.Len()
	for i, arg := range frame.GetArgs() {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(arg.GetName())
		b.WriteString(": ")
		writeSearchLiteral(&b, arg.GetValue(), &line.strings)
	}
	line.args.end = b.Len()
	b.WriteString(")")
	if typ := frame.GetType().GetNamedType(); typ != "" {
		b.WriteString(" -> ")
		b.WriteString(typ)
	}
	if recv := frame.GetReceiverDigest(); recv != "" {
		b.WriteString("  recv=")
		b.WriteString(recv)
	}
	line.text = b.String()
	return line
}

func writeSearchLiteral(b *strings.Builder, lit *callpbv1.Literal, ranges *[]callTextRange) {
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		b.WriteString(v.CallDigest)
	case *callpbv1.Literal_Null:
		b.WriteString("null")
	case *callpbv1.Literal_Bool:
		b.WriteString(strconv.FormatBool(v.Bool))
	case *callpbv1.Literal_Enum:
		b.WriteString(v.Enum)
	case *callpbv1.Literal_Int:
		b.WriteString(strconv.FormatInt(v.Int, 10))
	case *callpbv1.Literal_Float:
		b.WriteString(strconv.FormatFloat(v.Float, 'g', -1, 64))
	case *callpbv1.Literal_String_:
		quoted := strconv.Quote(v.String_)
		b.WriteByte('"')
		start := b.Len()
		b.WriteString(quoted[1 : len(quoted)-1])
		*ranges = append(*ranges, callTextRange{start, b.Len()})
		b.WriteByte('"')
	case *callpbv1.Literal_DigestedString:
		b.WriteString(call.DisplayDigestedString(v.DigestedString.GetValue(), digest.Digest(v.DigestedString.GetDigest())))
	case *callpbv1.Literal_Bytes:
		b.WriteString(call.DisplayBytes(v.Bytes))
	case *callpbv1.Literal_List:
		b.WriteByte('[')
		for i, val := range v.List.GetValues() {
			if i > 0 {
				b.WriteString(", ")
			}
			writeSearchLiteral(b, val, ranges)
		}
		b.WriteByte(']')
	case *callpbv1.Literal_Object:
		b.WriteByte('{')
		for i, val := range v.Object.GetValues() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(val.GetName())
			b.WriteString(": ")
			writeSearchLiteral(b, val.GetValue(), ranges)
		}
		b.WriteByte('}')
	default:
		b.WriteString("?")
	}
}
