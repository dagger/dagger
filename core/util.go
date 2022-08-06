package core

import (
	"encoding/json"
	"strings"
)

func convertArg(arg any, dest any) error {
	marshalled, err := json.Marshal(arg)
	if err != nil {
		return err
	}
	return json.Unmarshal(marshalled, dest)
}

func truncate(s string, args map[string]any) string {
	lines, ok := args["lines"].(int)
	if !ok {
		return s
	}
	l := strings.SplitN(s, "\n", lines+1)
	if lines > len(l) {
		lines = len(l)
	}
	return strings.Join(l[0:lines], "\n")
}
