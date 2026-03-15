package idtui

// partialJSONFields extracts top-level string fields from a potentially
// incomplete JSON object. It is designed for incremental/streaming JSON
// from LLM tool call arguments: the input may be truncated at any point
// (e.g. `{"path": "/foo", "content": "hel`).
//
// Only string values are extracted; nested objects, arrays, numbers, and
// booleans are skipped. The returned map contains only fields whose values
// have been fully parsed (i.e. the closing quote was seen). A field whose
// value is still being streamed is not included.
//
// This is intentionally simple and only handles the subset of JSON that
// LLM tool call arguments produce (flat objects with string values).
func partialJSONFields(s string) map[string]string {
	result := make(map[string]string)
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

		// Check if value is a string
		if i < n && s[i] == '"' {
			val, valComplete := parseString()
			if valComplete {
				result[key] = val
			}
			// If incomplete, we don't include the partial value
		} else {
			// Non-string value — skip it
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
