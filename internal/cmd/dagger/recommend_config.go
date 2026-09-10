package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	toml "github.com/pelletier/go-toml"
)

// recommendConfigFiles checks the contents of files in the recommendation scan.
// The predicate decides whether the contents match. Read errors stop the scan.
func recommendConfigFiles(ctx context.Context, ws *dagger.Workspace, pattern string, match func(string) bool) ([]string, error) {
	paths, err := SimpleRecommend(pattern)(ctx, ws)
	if err != nil {
		return nil, err
	}
	dir := recommendationDirectory(ws)
	var matches []string
	for _, path := range paths {
		contents, err := dir.File(path).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if match(contents) {
			matches = append(matches, path)
		}
	}
	return matches, nil
}

func hasMochaProperty(contents string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &object); err != nil {
		return false
	}
	_, found := object["mocha"]
	return found
}

func hasTOMLTable(name string) func(string) bool {
	return func(contents string) bool {
		tree, err := toml.Load(contents)
		if err != nil {
			return false
		}
		_, found := tree.Get(name).(*toml.Tree)
		return found
	}
}

func hasINISection(name string) func(string) bool {
	return func(contents string) bool {
		for line := range strings.SplitSeq(contents, "\n") {
			// An indented line can be part of a multiline option value.
			rest, found := strings.CutPrefix(line, "["+name+"]")
			if !found {
				continue
			}
			rest = strings.TrimSpace(rest)
			if rest == "" || strings.HasPrefix(rest, "#") || strings.HasPrefix(rest, ";") {
				return true
			}
		}
		return false
	}
}
