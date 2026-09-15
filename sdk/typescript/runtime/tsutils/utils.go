package tsutils

import (
	"bytes"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func appendIfNotExists(jsonStr, path string, value string) (string, error) {
	arr := gjson.Get(jsonStr, path).Array()

	// Check if value exists
	for _, v := range arr {
		if v.String() == value {
			// Already exists, do nothing
			return jsonStr, nil
		}
	}

	// Append since it's not found
	return sjson.Set(jsonStr, path+".-1", value)
}

func setIfNotExists(jsonStr, path string, value any) (string, error) {
	// Check if key exists
	if gjson.Get(jsonStr, path).Exists() {
		// Key already exists → no change
		return jsonStr, nil
	}

	// Otherwise, set it
	return sjson.Set(jsonStr, path, value)
}

func removeJSONComments(input string) string {
	var out bytes.Buffer
	inString := false
	escaped := false
	runes := []rune(input)

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if c == '"' && !escaped {
			inString = !inString
		}

		if !inString && c == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			// skip until newline
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			out.WriteRune('\n')
			continue
		}

		out.WriteRune(c)
		escaped = (c == '\\' && !escaped)
	}

	return out.String()
}
