// Package pragma parses Dagger "pragma" comments — the `+key=value`
// metadata that module authors attach to functions, parameters, fields,
// and enum members to influence codegen and module registration.
//
// Both the legacy packages.Load-based templates and the AST-based
// astscan path read pragmas, so the parser lives here to keep the two
// codepaths in sync.
package pragma

import (
	"encoding/json"
	"regexp"
	"strings"
)

// commentRegex matches a single pragma directive: a leading `+`, a key,
// and an optional `=value` suffix terminated by EOL.
var commentRegex = regexp.MustCompile(`[ \t]*\+[ \t]*(\S+?)(?:(=[ \t]*)|(?:\r?\n|$))`)

// Parse extracts pragmas from a comment text and returns the parsed
// key→value map plus the comment text with pragma directives removed.
//
// Values are JSON-decoded when possible (so `+default=42` → float64(42),
// `+default="hi"` → string "hi", `+default=[1,2]` → []any{...}). Values
// that don't parse as JSON are returned verbatim as strings.
func Parse(comment string) (map[string]any, string) {
	data := map[string]any{}
	rest := ""
	lastEnd := 0
	for _, v := range commentRegex.FindAllStringSubmatchIndex(comment, -1) {
		// Skip matches that overlap an already-consumed region (the
		// previous pragma's value may have spanned multiple lines).
		if v[0] < lastEnd {
			continue
		}

		var key string
		if v[2] != -1 {
			key = comment[v[2]:v[3]]
		}

		var value any
		end := v[1]
		if v[4] != -1 {
			dec := json.NewDecoder(strings.NewReader(comment[v[5]:]))
			if err := dec.Decode(&value); err == nil {
				// JSON decoded successfully (possibly across lines):
				// consume the value and the rest of the line.
				end = v[5] + int(dec.InputOffset())
				idx := strings.IndexAny(comment[end:], "\n")
				if idx == -1 {
					end = len(comment)
				} else {
					end += idx + 1
				}
			} else {
				// Not valid JSON; treat the rest of the line as a raw
				// string value.
				idx := strings.IndexAny(comment[v[5]:], "\n")
				var valueStr string
				if idx == -1 {
					valueStr = comment[v[5]:]
					end = len(comment)
				} else {
					idx += v[5]
					valueStr = strings.TrimSuffix(comment[v[5]:idx], "\r")
					end = idx + 1
				}
				if len(valueStr) == 0 {
					value = nil
				} else {
					value = valueStr
				}
			}
		}

		data[key] = value
		rest += comment[lastEnd:v[0]]
		lastEnd = end
	}
	rest += comment[lastEnd:]

	return data, rest
}
