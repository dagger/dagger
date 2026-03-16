package idtui

import "strings"

// parsedField holds a parsed field value and whether it is complete
// (closing quote seen) or still streaming.
type parsedField struct {
	Key      string
	Value    string
	Complete bool
}

// partialJSONFields extracts top-level string fields from a potentially
// incomplete JSON object. It is designed for incremental/streaming JSON
// from LLM tool call arguments: the input may be truncated at any point
// (e.g. `{"path": "/foo", "content": "hel`).
//
// String values are extracted as soon as any content is available, even
// if the closing quote hasn't arrived yet (streaming). The Complete flag
// indicates whether the value is fully parsed. String arrays (e.g.
// ["a", "b"]) are joined with spaces. Other value types (nested objects,
// numbers, booleans) are skipped.
//
// This is intentionally simple and only handles the subset of JSON that
// LLM tool call arguments produce.
func partialJSONFields(s string) []parsedField {
	var result []parsedField
	i := 0
	n := len(s)

	// Skip whitespace
	skip := func() {
		for i < n && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t') {
			i++
		}
	}

	// Parse a JSON string, handling escape sequences.
	// Returns the string content and true if the closing quote was found,
	// or the partial content and false if truncated.
	parseString := func() (string, bool) {
		if i >= n || s[i] != '"' {
			return "", false
		}
		i++ // skip opening quote
		var buf []byte
		for i < n {
			ch := s[i]
			if ch == '\\' {
				i++
				if i >= n {
					return string(buf), false
				}
				esc := s[i]
				switch esc {
				case '"', '\\', '/':
					buf = append(buf, esc)
				case 'n':
					buf = append(buf, '\n')
				case 'r':
					buf = append(buf, '\r')
				case 't':
					buf = append(buf, '\t')
				case 'b':
					buf = append(buf, '\b')
				case 'f':
					buf = append(buf, '\f')
				case 'u':
					// Skip \uXXXX sequences — just pass them through raw
					buf = append(buf, '\\', 'u')
					i++
					for j := 0; j < 4 && i < n; j++ {
						buf = append(buf, s[i])
						i++
					}
					continue
				default:
					buf = append(buf, '\\', esc)
				}
				i++
				continue
			}
			if ch == '"' {
				i++ // skip closing quote
				return string(buf), true
			}
			buf = append(buf, ch)
			i++
		}
		return string(buf), false
	}

	// Skip a JSON value (string, number, object, array, bool, null).
	// Returns true if the value was fully consumed, false if truncated.
	var skipValue func() bool
	skipValue = func() bool {
		skip()
		if i >= n {
			return false
		}
		switch s[i] {
		case '"':
			_, complete := parseString()
			return complete
		case '{':
			i++
			depth := 1
			for i < n && depth > 0 {
				if s[i] == '{' {
					depth++
				} else if s[i] == '}' {
					depth--
				} else if s[i] == '"' {
					parseString() // skip strings to handle { and } inside them
					continue
				}
				i++
			}
			return depth == 0
		case '[':
			i++
			depth := 1
			for i < n && depth > 0 {
				if s[i] == '[' {
					depth++
				} else if s[i] == ']' {
					depth--
				} else if s[i] == '"' {
					parseString()
					continue
				}
				i++
			}
			return depth == 0
		default:
			// number, bool, null — scan until delimiter
			for i < n && s[i] != ',' && s[i] != '}' && s[i] != ']' &&
				s[i] != ' ' && s[i] != '\n' && s[i] != '\r' && s[i] != '\t' {
				i++
			}
			return true
		}
	}

	// Expect opening brace
	skip()
	if i >= n || s[i] != '{' {
		return result
	}
	i++ // skip {

	for {
		skip()
		if i >= n || s[i] == '}' {
			break
		}

		// Parse key
		key, keyComplete := parseString()
		if !keyComplete {
			break
		}

		// Expect colon
		skip()
		if i >= n || s[i] != ':' {
			break
		}
		i++ // skip :
		skip()

		// Check value type
		if i < n && s[i] == '"' {
			// String value — include even if truncated (streaming)
			val, complete := parseString()
			if val != "" {
				result = append(result, parsedField{Key: key, Value: val, Complete: complete})
			}
		} else if i < n && s[i] == '[' {
			// Array value — try to extract as string array
			saveI := i
			i++ // skip [
			var elems []string
			complete := false
			for {
				skip()
				if i >= n {
					break
				}
				if s[i] == ']' {
					i++
					complete = true
					break
				}
				if s[i] == '"' {
					elem, ok := parseString()
					if !ok {
						break
					}
					elems = append(elems, elem)
					skip()
					if i < n && s[i] == ',' {
						i++
					}
				} else {
					// Non-string element — not a string array, skip the whole thing
					i = saveI
					if !skipValue() {
						// truncated
					}
					complete = false
					elems = nil
					break
				}
			}
			if complete && len(elems) > 0 {
				result = append(result, parsedField{Key: key, Value: strings.Join(elems, " "), Complete: true})
			}
		} else {
			// Other value — skip it
			if !skipValue() {
				break
			}
		}

		// Skip comma
		skip()
		if i < n && s[i] == ',' {
			i++
		}
	}

	return result
}
