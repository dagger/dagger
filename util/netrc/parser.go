package netrc

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
	"unicode"
)

type NetrcEntry struct {
	Machine  string
	Login    string
	Password string
}

func NetrcEntries(in io.Reader) iter.Seq[NetrcEntry] {
	scanner := bufio.NewScanner(in)
	scanner.Split(scanWordsWithQuotes)
	return func(yield func(NetrcEntry) bool) {
		var entry NetrcEntry
		for scanner.Scan() {
			key := strings.TrimSpace(scanner.Text())
			if key == "" {
				continue
			}

			switch key {
			case "machine":
				if entry != (NetrcEntry{}) {
					if !yield(entry) {
						return
					}
				}
				if !scanner.Scan() {
					return
				}
				entry = NetrcEntry{Machine: scanner.Text()}
			case "default":
				if entry != (NetrcEntry{}) {
					if !yield(entry) {
						return
					}
				}
				entry = NetrcEntry{}
			case "login":
				if !scanner.Scan() {
					return
				}
				entry.Login = scanner.Text()
			case "password":
				if !scanner.Scan() {
					return
				}
				entry.Password = scanner.Text()
			case "macdef":
				// not supported, skip until null token
				for scanner.Scan() {
					if scanner.Text() == "\n\n" {
						break
					}
				}
			default: // ignore unknown
			}
		}
		if entry != (NetrcEntry{}) {
			yield(entry)
		}
	}
}

// scanWordsWithQuotes is a bufio.SplitFunc that splits words, handling quoted strings.
// Words are separated by spaces, but quoted strings (enclosed in ") are returned as a single word.
func scanWordsWithQuotes(data []byte, atEOF bool) (advance int, token []byte, err error) {
	// Skip leading spaces
	start := 0
	for start < len(data) && unicode.IsSpace(rune(data[start])) && data[start] != '\n' {
		start++
	}
	if start >= len(data) {
		if atEOF {
			return 0, nil, nil
		}
		return 0, nil, nil
	}

	switch data[start] {
	case '"':
		// quoted string
		end := start + 1
		for end < len(data) && data[end] != '"' {
			if data[end] == '\\' {
				end++ // next char is literal, skip it
			}
			end++
		}
		if end < len(data) {
			s, err := strconv.Unquote(string(data[start : end+1]))
			return end + 1, []byte(s), err
		}
		// If atEOF and no closing quote, return rest
		if atEOF {
			return 0, nil, fmt.Errorf("no closing quote")
		}
		return 0, nil, nil
	case '\n':
		// newline
		end := start + 1
		for end < len(data) && data[end] == '\n' {
			end++
		}
		return end, data[start:end], nil
	default:
		// unquoted word
		end := start + 1
		for end < len(data) && !unicode.IsSpace(rune(data[end])) {
			end++
		}
		return end, data[start:end], nil
	}
}
