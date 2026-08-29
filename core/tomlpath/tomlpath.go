// Package tomlpath formats TOML dotted-key path segments.
//
// It is the single source of truth for turning a logical key (one path
// segment) into its TOML representation, quoting and escaping it when it is
// not a bare key. Both the workspace config writer and the TOMLValue editor
// use it so that the same logical edit always produces identical TOML output.
package tomlpath

import (
	"fmt"
	"strings"
)

// FormatSegment formats one TOML dotted-key path segment, quoting and escaping
// it when it is not a bare key.
func FormatSegment(segment string) string {
	if IsBareSegment(segment) {
		return segment
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range segment {
		switch r {
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Dotted builds a TOML dotted-key path from logical segments, formatting each
// one with FormatSegment.
func Dotted(segments ...string) string {
	parts := make([]string, len(segments))
	for i, seg := range segments {
		parts[i] = FormatSegment(seg)
	}
	return strings.Join(parts, ".")
}

// IsBareSegment reports whether segment can be written as an unquoted TOML key.
func IsBareSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if !isBareKeyChar(segment[i]) {
			return false
		}
	}
	return true
}

func isBareKeyChar(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z' ||
		'0' <= c && c <= '9' ||
		c == '_' ||
		c == '-'
}
