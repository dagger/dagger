package dagui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// GrepCalls searches every call payload this client has ingested by content:
// each call is rendered as one line — digest, field, arguments, return type,
// receiver digest — with ordinary literals untruncated and digested strings
// opaque. Matching lines are returned sorted and capped at max.
//
// This is the discovery half of the digest→ID path: an engine error names
// bare digests ("load xxh3:…: inputs: …"), and CallIDForDigest can rebuild a
// handle from one — but nothing else answers "which call mentions this path /
// image / module?". Truncation would defeat exactly that: a host path baked
// deep into an ordinary argument must be greppable even though every display
// path truncates literals (extract.go's literalLabel). Digested strings are
// deliberately opaque in both paths.
func (db *DB) GrepCalls(re *regexp.Regexp, max int) []string {
	var lines []string
	for dig := range db.CallPayloads {
		call := db.Call(dig)
		if call == nil {
			continue
		}
		line := renderCallLine(call)
		if !re.MatchString(line) {
			continue
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	if max > 0 && len(lines) > max {
		lines = append(lines[:max], fmt.Sprintf("… %d more (raise the cap or tighten the pattern)", len(lines)-max))
	}
	return lines
}

// renderCallLine renders one call for content search:
//
//	<digest>  <field>(<args>) -> <Type>  recv=<receiverDigest>
//
// Receiver and ID-argument digests are printed verbatim so a chain can be
// walked by grepping for the digest a line names.
func renderCallLine(call *callpbv1.Call) string {
	var b strings.Builder
	b.WriteString(call.GetDigest())
	b.WriteString("  ")
	b.WriteString(call.GetField())
	b.WriteString("(")
	for i, arg := range call.GetArgs() {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(arg.GetName())
		b.WriteString(": ")
		b.WriteString(fullLiteral(arg.GetValue()))
	}
	b.WriteString(")")
	if typ := call.GetType().GetNamedType(); typ != "" {
		b.WriteString(" -> ")
		b.WriteString(typ)
	}
	if recv := call.GetReceiverDigest(); recv != "" {
		b.WriteString("  recv=")
		b.WriteString(recv)
	}
	return b.String()
}

// fullLiteral is literalLabel without ordinary-string truncation. Digested
// strings stay opaque in both renderers.
func fullLiteral(lit *callpbv1.Literal) string {
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return v.CallDigest
	case *callpbv1.Literal_Null:
		return "null"
	case *callpbv1.Literal_Bool:
		return strconv.FormatBool(v.Bool)
	case *callpbv1.Literal_Enum:
		return v.Enum
	case *callpbv1.Literal_Int:
		return strconv.FormatInt(v.Int, 10)
	case *callpbv1.Literal_Float:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case *callpbv1.Literal_String_:
		return strconv.Quote(v.String_)
	case *callpbv1.Literal_DigestedString:
		return call.DisplayDigestedString(
			v.DigestedString.GetValue(),
			digest.Digest(v.DigestedString.GetDigest()),
		)
	case *callpbv1.Literal_List:
		vals := v.List.GetValues()
		parts := make([]string, 0, len(vals))
		for _, val := range vals {
			parts = append(parts, fullLiteral(val))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *callpbv1.Literal_Object:
		vals := v.Object.GetValues()
		parts := make([]string, 0, len(vals))
		for _, val := range vals {
			parts = append(parts, fmt.Sprintf("%s: %s", val.GetName(), fullLiteral(val.GetValue())))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return "?"
	}
}
