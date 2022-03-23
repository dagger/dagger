// Package unquote provides a function to unquote txtpb-formatted quoted string literals.
package unquote

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/protocolbuffers/txtpbfmt/ast"
)

// Unquote returns the value of the string node.
// Calling Unquote on non-string node doesn't panic, but is otherwise undefined.
func Unquote(n *ast.Node) (string, error) {
	var ret strings.Builder
	for _, v := range n.Values {
		uq, err := unquote(v.Value)
		if err != nil {
			return "", err
		}
		ret.WriteString(uq)
	}
	return ret.String(), nil
}

func unquote(s string) (string, error) {
	if len(s) < 2 {
		return "", errors.New("not a quoted string")
	}
	quote := s[0]
	if quote != '"' && quote != '\'' {
		return "", errors.New("invalid quote character")
	}
	if s[len(s)-1] != quote {
		return "", errors.New("unmatched quote")
	}
	return unquoteC(s[1:len(s)-1], rune(quote))
}

var (
	errBadUTF8 = errors.New("bad UTF-8")
)

func unquoteC(s string, quote rune) (string, error) {
	// Copied from third_party/golang/protobuf/proto/text_parser.go

	// This is based on C++'s tokenizer.cc.
	// Despite its name, this is *not* parsing C syntax.
	// For instance, "\0" is an invalid quoted string.

	// Avoid allocation in trivial cases.
	simple := true
	for _, r := range s {
		if r == '\\' || r == quote {
			simple = false
			break
		}
	}
	if simple {
		return s, nil
	}

	buf := make([]byte, 0, 3*len(s)/2)
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			return "", errBadUTF8
		}
		s = s[n:]
		if r != '\\' {
			if r < utf8.RuneSelf {
				buf = append(buf, byte(r))
			} else {
				buf = append(buf, string(r)...)
			}
			continue
		}

		ch, tail, err := unescape(s)
		if err != nil {
			return "", err
		}
		buf = append(buf, ch...)
		s = tail
	}
	return string(buf), nil
}

func unescape(s string) (ch string, tail string, err error) {
	// Copied from third_party/golang/protobuf/proto/text_parser.go

	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && n == 1 {
		return "", "", errBadUTF8
	}
	s = s[n:]
	switch r {
	case 'a':
		return "\a", s, nil
	case 'b':
		return "\b", s, nil
	case 'f':
		return "\f", s, nil
	case 'n':
		return "\n", s, nil
	case 'r':
		return "\r", s, nil
	case 't':
		return "\t", s, nil
	case 'v':
		return "\v", s, nil
	case '?':
		return "?", s, nil // trigraph workaround
	case '\'', '"', '\\':
		return string(r), s, nil
	case '0', '1', '2', '3', '4', '5', '6', '7':
		if len(s) < 2 {
			return "", "", fmt.Errorf(`\%c requires 2 following digits`, r)
		}
		ss := string(r) + s[:2]
		s = s[2:]
		i, err := strconv.ParseUint(ss, 8, 8)
		if err != nil {
			return "", "", fmt.Errorf(`\%s contains non-octal digits`, ss)
		}
		return string([]byte{byte(i)}), s, nil
	case 'x', 'X', 'u', 'U':
		var n int
		switch r {
		case 'x', 'X':
			n = 2
		case 'u':
			n = 4
		case 'U':
			n = 8
		}
		if len(s) < n {
			return "", "", fmt.Errorf(`\%c requires %d following digits`, r, n)
		}
		ss := s[:n]
		s = s[n:]
		i, err := strconv.ParseUint(ss, 16, 64)
		if err != nil {
			return "", "", fmt.Errorf(`\%c%s contains non-hexadecimal digits`, r, ss)
		}
		if r == 'x' || r == 'X' {
			return string([]byte{byte(i)}), s, nil
		}
		if i > utf8.MaxRune {
			return "", "", fmt.Errorf(`\%c%s is not a valid Unicode code point`, r, ss)
		}
		return string(i), s, nil
	}
	return "", "", fmt.Errorf(`unknown escape \%c`, r)
}
