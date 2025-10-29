package tsutils

import (
	"bytes"
	"strings"

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
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		// remove everything after // (simple approach)
		if idx := strings.Index(strings.TrimSpace(line), "//"); idx >= 0 {
			line = line[:idx]
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}
