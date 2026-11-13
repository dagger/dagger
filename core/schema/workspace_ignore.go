package schema

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/moby/patternmatcher"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// loadWorkspaceIgnorePatterns reads ignore from the selected dagger.toml in
// root. A missing file or unparseable config yields no extra excludes.
// Other read errors (context cancel, I/O) are returned to the caller.
func loadWorkspaceIgnorePatterns(ctx context.Context, root dagql.ObjectResult[*core.Directory], configFile string) ([]string, error) {
	if configFile == "" {
		return nil, nil
	}
	data, err := core.DirectoryReadFile(ctx, root, filepath.ToSlash(configFile))
	return workspaceIgnoreFromConfigBytes(data, configFile, err)
}

func workspaceIgnoreFromConfigBytes(data []byte, configFile string, err error) ([]string, error) {
	if err != nil {
		if isAbsentWorkspaceConfig(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := workspace.ParseConfig(data)
	if err != nil {
		// A broken dagger.toml adds no excludes; do not fail workspace load.
		return nil, nil //nolint:nilerr
	}
	return workspace.IgnorePatterns(configFile, cfg.Ignore), nil
}

func isAbsentWorkspaceConfig(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

func workspaceFilterWithIgnore(ws *core.Workspace, resolvedPath string, filter core.CopyFilter) core.CopyFilter {
	if ws == nil {
		return filter
	}
	ignore := workspaceIgnoreForPath(ws.IgnorePatterns(), resolvedPath)
	if len(ignore) == 0 {
		return filter
	}
	// User ignore comes first so a dagger.toml `!pattern` cannot re-include a
	// path the engine already excluded.
	filter.Exclude = append(append([]string(nil), ignore...), filter.Exclude...)
	return filter
}

func workspaceIgnoreForPath(ignore []string, resolvedPath string) []string {
	if len(ignore) == 0 {
		return nil
	}
	resolvedPath = path.Clean(filepath.ToSlash(resolvedPath))
	if resolvedPath == "" || resolvedPath == "/" {
		resolvedPath = "."
	}
	if resolvedPath == "." {
		return append([]string(nil), ignore...)
	}

	covered := workspaceReadPathIgnored(ignore, resolvedPath)
	patterns := make([]string, 0, len(ignore)+1)
	if covered {
		patterns = append(patterns, "*")
	}
	for _, pattern := range ignore {
		for _, rel := range workspaceIgnorePatternForPath(pattern, resolvedPath) {
			if rel == "*" && covered {
				continue
			}
			patterns = append(patterns, rel)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return patterns
}

func workspaceReadPathIgnored(ignore []string, resolvedPath string) bool {
	pm, err := patternmatcher.New(ignore)
	if err != nil {
		return false
	}
	ok, err := pm.MatchesOrParentMatches(resolvedPath)
	return err == nil && ok
}

func workspaceIgnorePatternForPath(pattern, resolvedPath string) []string {
	pattern, negative := strings.CutPrefix(pattern, "!")
	if pattern == "" {
		return nil
	}

	var rels []string
	switch {
	case pattern == resolvedPath:
		rels = []string{"*"}
	case strings.HasPrefix(pattern, resolvedPath+"/"):
		rels = []string{strings.TrimPrefix(pattern, resolvedPath+"/")}
	default:
		rels = workspaceIgnoreStripGlobPrefix(pattern, resolvedPath)
	}
	if len(rels) == 0 {
		return nil
	}
	if negative {
		for i, rel := range rels {
			rels[i] = "!" + rel
		}
	}
	return rels
}

func workspaceIgnoreStripGlobPrefix(pattern, resolvedPath string) []string {
	pSegs := strings.Split(pattern, "/")
	rSegs := strings.Split(resolvedPath, "/")
	i, j := 0, 0
	for i < len(pSegs) && j < len(rSegs) {
		if pSegs[i] == "**" {
			return workspaceIgnoreStarstarRebase(pSegs[i+1:], rSegs[j:])
		}
		ok, err := path.Match(pSegs[i], rSegs[j])
		if err != nil || !ok {
			return nil
		}
		i++
		j++
	}
	if i == 0 || j != len(rSegs) {
		return nil
	}
	if i == len(pSegs) {
		return []string{"*"}
	}
	return []string{strings.Join(pSegs[i:], "/")}
}

func workspaceIgnoreStarstarRebase(tail, readSegs []string) []string {
	remainder := strings.Join(tail, "/")
	var out []string
	if remainder == "" {
		out = append(out, "**")
	} else {
		out = append(out, "**/"+remainder)
		out = append(out, workspaceIgnoreStarstarLeftovers(tail, readSegs)...)
	}
	return out
}

func workspaceIgnoreStarstarLeftovers(tail, readSegs []string) []string {
	if len(tail) == 0 || len(readSegs) == 0 {
		return nil
	}
	remainder := strings.Join(tail, "/")
	var out []string
	for start := 0; start < len(readSegs); start++ {
		suffix := strings.Join(readSegs[start:], "/")
		out = append(out, workspaceIgnoreStripGlobPrefix(remainder, suffix)...)
	}
	return out
}
