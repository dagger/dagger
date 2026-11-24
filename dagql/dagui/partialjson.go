package dagui

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

// partialJSON is a JSON object decoded from a document that may still be
// streaming: the best-effort value so far.
type partialJSON struct {
	// Value is the object decoded so far: every member that arrived whole,
	// plus the string the document ends in the middle of, if any (see
	// Truncated). Nil when not even an object's opening brace has arrived.
	Value map[string]any
	// Complete reports whether the document's object is closed. Anything
	// after it is ignored.
	Complete bool
	// Truncated is the path (object keys, and array indexes in decimal) to
	// the string value the document ends in the middle of, which Value holds
	// as far as it got; nil if there is none.
	Truncated []string
}

// decodePartialJSON decodes doc, a JSON object that may be cut short at any
// byte, into the members it holds so far. It closes an unterminated string
// value and the objects and arrays around it, and leaves out what can't be
// told yet: a key with no value, a number or literal that may still grow,
// and a partial escape at the end of a string.
//
// It costs a pass over doc plus a json.Unmarshal of (at most) doc, so
// callers decode once per change, not per read.
func decodePartialJSON(doc string) partialJSON {
	var whole map[string]any
	if err := json.Unmarshal([]byte(doc), &whole); err == nil {
		if whole == nil {
			// "null"
			return partialJSON{}
		}
		return partialJSON{Value: whole, Complete: true}
	}
	repaired, complete, truncated, truncatedAt := repairJSON(doc)
	if repaired == "" {
		return partialJSON{}
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(repaired), &value); err != nil {
		if truncated == nil {
			return partialJSON{}
		}
		// The string the document ends in doesn't decode: leave it out,
		// and keep the members before it.
		repaired, _, _, _ = repairJSON(doc[:truncatedAt])
		value = nil
		if repaired == "" || json.Unmarshal([]byte(repaired), &value) != nil {
			return partialJSON{}
		}
		return partialJSON{Value: value}
	}
	return partialJSON{Value: value, Complete: complete, Truncated: truncated}
}

type jsonFrame struct {
	array bool
	state jsonState
	// key is the member being read, in an object; len counts the members or
	// elements so far.
	key string
	len int
}

type jsonState int

const (
	// expecting a key or the object's end, or a value or the array's end
	stateOpen jsonState = iota
	// expecting ':' after a key
	stateColon
	// expecting a member's value
	stateValue
	// expecting ',' or the end, after a value
	stateNext
)

// repairJSON returns the longest prefix of doc's top-level object that
// decodes, made a whole document: doc cut back to the last point at which
// every value so far was complete, with an unterminated string value kept
// and closed, and the open objects and arrays closed. It returns "" if doc
// doesn't open an object. complete reports whether the object was closed;
// truncated is the path to a string value cut short (see partialJSON), and
// truncatedAt where it starts in doc.
//
//nolint:gocyclo // streaming JSON scanner; a flat state machine is clearest here
func repairJSON(doc string) (repaired string, complete bool, truncated []string, truncatedAt int) {
	var stack []jsonFrame
	// The last point the document can be cut at, and closers to append.
	cut := -1
	var cutClosers string

	closers := func() string {
		var b strings.Builder
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].array {
				b.WriteByte(']')
			} else {
				b.WriteByte('}')
			}
		}
		return b.String()
	}
	mark := func(at int) {
		cut, cutClosers = at, closers()
	}
	path := func() []string {
		p := make([]string, 0, len(stack))
		for _, f := range stack {
			if f.array {
				p = append(p, strconv.Itoa(f.len))
			} else {
				p = append(p, f.key)
			}
		}
		return p
	}
	// valueDone advances past a value that ends just before at.
	valueDone := func(at int) bool {
		if len(stack) == 0 {
			return true
		}
		top := &stack[len(stack)-1]
		top.len++
		top.state = stateNext
		mark(at)
		return false
	}
	result := func() (string, bool, []string, int) {
		if cut < 0 {
			return "", false, nil, 0
		}
		return doc[:cut] + cutClosers, false, nil, 0
	}

	i := 0
	for {
		for i < len(doc) && isJSONSpace(doc[i]) {
			i++
		}
		if i >= len(doc) {
			return result()
		}
		c := doc[i]
		expectValue := len(stack) == 0 ||
			stack[len(stack)-1].state == stateValue ||
			(stack[len(stack)-1].array && stack[len(stack)-1].state == stateOpen)
		if len(stack) > 0 && !expectValue {
			top := &stack[len(stack)-1]
			switch {
			case top.state == stateOpen && c == '"':
				// a key
				end, ok := scanJSONString(doc, i)
				if !ok {
					return result()
				}
				var key string
				if err := json.Unmarshal([]byte(doc[i:end]), &key); err != nil {
					return result()
				}
				top.key = key
				top.state = stateColon
				i = end
			case top.state == stateColon && c == ':':
				top.state = stateValue
				i++
			case top.state == stateNext && c == ',':
				top.state = stateOpen
				if top.array {
					top.state = stateValue
				}
				i++
			case c == '}' && !top.array &&
				(top.state == stateNext || (top.state == stateOpen && top.len == 0)):
				stack = stack[:len(stack)-1]
				i++
				if valueDone(i) {
					return doc[:i], true, nil, 0
				}
			case top.state == stateNext && c == ']' && top.array:
				stack = stack[:len(stack)-1]
				i++
				if valueDone(i) {
					return doc[:i], true, nil, 0
				}
			default:
				// not JSON: keep what came before
				return result()
			}
			continue
		}

		// a value
		if len(stack) == 0 && c != '{' {
			// only an object will do
			return "", false, nil, 0
		}
		switch {
		case c == '{' || c == '[':
			stack = append(stack, jsonFrame{array: c == '['})
			i++
			mark(i)
		case c == ']' && stack[len(stack)-1].array && stack[len(stack)-1].state == stateOpen:
			// an empty array
			stack = stack[:len(stack)-1]
			i++
			if valueDone(i) {
				return doc[:i], true, nil, 0
			}
		case c == '"':
			end, ok := scanJSONString(doc, i)
			if !ok {
				// The document ends in this string: keep it, as far as it
				// decodes.
				return doc[:i] + trimPartialString(doc[i:]) + `"` + closers(), false, path(), i
			}
			i = end
			if valueDone(i) {
				return doc[:i], true, nil, 0
			}
		default:
			end := i
			for end < len(doc) && isJSONScalarByte(doc[end]) {
				end++
			}
			if end == i || end == len(doc) {
				// not JSON, or a number or literal that may not be done
				return result()
			}
			if !json.Valid([]byte(doc[i:end])) {
				return result()
			}
			i = end
			if valueDone(i) {
				return doc[:i], true, nil, 0
			}
		}
	}
}

// scanJSONString returns the end of the JSON string starting at doc[start]
// (a '"'), just past its closing quote, or false if doc ends first.
func scanJSONString(doc string, start int) (int, bool) {
	for i := start + 1; i < len(doc); i++ {
		switch doc[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

// trimPartialString trims an unterminated JSON string (from its opening
// quote) back to what decodes: a dangling backslash, a \u escape missing
// hex digits, a high surrogate missing its low half, or a UTF-8 sequence
// missing bytes.
func trimPartialString(s string) string {
	end := len(s)
	lastEscape := -1 // start of the last complete \uXXXX escape
	for i := 1; i < len(s); i++ {
		if s[i] < 0x20 {
			// A raw control character can't be in a JSON string: whatever
			// follows wasn't written as part of it (e.g. the newline
			// displayPhases.Close ends a cut-off stream with).
			end = i
			break
		}
		if s[i] != '\\' {
			continue
		}
		if i+1 >= len(s) {
			end = i
			break
		}
		if s[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(s) {
			end = i
			break
		}
		lastEscape = i
		i += 5
	}
	s = s[:end]
	if lastEscape >= 0 && lastEscape+6 == len(s) {
		if r, err := strconv.ParseUint(s[lastEscape+2:lastEscape+6], 16, 16); err == nil &&
			r >= 0xD800 && r < 0xDC00 {
			s = s[:lastEscape]
		}
	}
	// A chunk boundary can split a multibyte character.
	for n := 0; n < utf8.UTFMax-1 && len(s) > 1; n++ {
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// isJSONScalarByte reports whether c can be part of a number or a literal
// (true, false, null).
func isJSONScalarByte(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z':
		return true
	}
	return c == '-' || c == '+' || c == '.' || c == 'E'
}
