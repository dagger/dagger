package workspace

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// maxGitConfigIncludeDepth mirrors git's own include recursion limit.
const maxGitConfigIncludeDepth = 10

// GitConfigState carries the repository state used to resolve git config
// include directives and evaluate includeIf conditions.
type GitConfigState struct {
	// ConfigPath is the path of the config file being expanded; relative
	// include paths resolve against its directory.
	ConfigPath string
	// GitDir is the repository's $GIT_DIR (the per-worktree gitdir for linked
	// worktrees), matched by gitdir/gitdir:i conditions.
	GitDir string
	// Branch is the current short branch name, matched by onbranch
	// conditions. Empty when detached or unknown.
	Branch string
}

// ResolveGitConfigIncludes expands include and includeIf directives in git
// config contents the way git does when reading configuration: each included
// file is inlined at the location of its directive, recursively, up to git's
// depth limit. Includes that cannot be read and includeIf sections whose
// condition does not match are skipped. Conditions with home-relative (~)
// patterns and hasconfig conditions are treated as non-matching, since the
// caller's home directory and full config set are not available here.
func ResolveGitConfigIncludes(
	ctx context.Context,
	readFile func(context.Context, string) ([]byte, error),
	state GitConfigState,
	data []byte,
) []byte {
	return resolveGitConfigIncludes(ctx, readFile, state, data, maxGitConfigIncludeDepth)
}

func resolveGitConfigIncludes(
	ctx context.Context,
	readFile func(context.Context, string) ([]byte, error),
	state GitConfigState,
	data []byte,
	depth int,
) []byte {
	if depth <= 0 || readFile == nil {
		return data
	}

	var b strings.Builder
	includeActive := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))

		if strings.HasPrefix(trimmed, "[") {
			includeActive = gitConfigIncludeSectionMatches(trimmed, state)
			b.WriteString(line)
			continue
		}

		if includeActive {
			key, value, ok := strings.Cut(trimmed, "=")
			if ok && strings.TrimSpace(key) == "path" {
				includePath := strings.Trim(strings.TrimSpace(value), `"`)
				if included, ok := readGitConfigInclude(ctx, readFile, state.ConfigPath, includePath); ok {
					included = resolveGitConfigIncludes(ctx, readFile, GitConfigState{
						ConfigPath: includePathResolved(state.ConfigPath, includePath),
						GitDir:     state.GitDir,
						Branch:     state.Branch,
					}, included, depth-1)
					b.Write(included)
					if len(included) > 0 && included[len(included)-1] != '\n' {
						b.WriteString("\n")
					}
				}
				continue
			}
		}

		b.WriteString(line)
	}
	return []byte(b.String())
}

func includePathResolved(configPath, includePath string) string {
	if includePath == "" || strings.HasPrefix(includePath, "~") {
		return includePath
	}
	if filepath.IsAbs(includePath) {
		return filepath.Clean(includePath)
	}
	return filepath.Join(filepath.Dir(configPath), includePath)
}

func readGitConfigInclude(
	ctx context.Context,
	readFile func(context.Context, string) ([]byte, error),
	configPath, includePath string,
) ([]byte, bool) {
	if includePath == "" || strings.HasPrefix(includePath, "~") {
		// The caller's home directory cannot be resolved here.
		return nil, false
	}
	data, err := readFile(ctx, includePathResolved(configPath, includePath))
	if err != nil {
		return nil, false
	}
	return data, true
}

// gitConfigIncludeSectionMatches reports whether a section header line starts
// an include section whose contents should be expanded: [include], or
// [includeIf "cond"] with a matching condition.
func gitConfigIncludeSectionMatches(header string, state GitConfigState) bool {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "["), "]"))
	name, rest, _ := strings.Cut(inner, " ")
	switch strings.ToLower(name) {
	case "include":
		return rest == ""
	case "includeif":
		cond := strings.Trim(strings.TrimSpace(rest), `"`)
		return gitConfigIncludeCondMatches(cond, state)
	}
	return false
}

func gitConfigIncludeCondMatches(cond string, state GitConfigState) bool {
	switch {
	case strings.HasPrefix(cond, "gitdir:"):
		return gitDirPatternMatches(strings.TrimPrefix(cond, "gitdir:"), state, false)
	case strings.HasPrefix(cond, "gitdir/i:"):
		return gitDirPatternMatches(strings.TrimPrefix(cond, "gitdir/i:"), state, true)
	case strings.HasPrefix(cond, "onbranch:"):
		return onBranchPatternMatches(strings.TrimPrefix(cond, "onbranch:"), state.Branch)
	}
	// hasconfig and unknown conditions are not evaluated here.
	return false
}

// gitDirPatternMatches applies git's gitdir pattern transformations: a
// leading ./ resolves against the config file's directory, patterns with no
// absolute anchor get **/ prepended, and a trailing / matches everything
// below. Home-relative (~) patterns are treated as non-matching.
func gitDirPatternMatches(pattern string, state GitConfigState, insensitive bool) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || state.GitDir == "" {
		return false
	}
	if strings.HasPrefix(pattern, "~") {
		return false
	}
	if strings.HasPrefix(pattern, "./") {
		pattern = filepath.ToSlash(filepath.Dir(state.ConfigPath)) + pattern[1:]
	}
	if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") {
		pattern = "**/" + pattern
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}

	gitDir := filepath.ToSlash(filepath.Clean(state.GitDir))
	if insensitive {
		pattern = strings.ToLower(pattern)
		gitDir = strings.ToLower(gitDir)
	}
	ok, err := doublestar.Match(pattern, gitDir)
	return err == nil && ok
}

func onBranchPatternMatches(pattern, branch string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || branch == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	ok, err := doublestar.Match(pattern, branch)
	return err == nil && ok
}

// GitBranchFromHEAD parses .git/HEAD contents into the current short branch
// name. A detached HEAD (or anything else) yields "".
func GitBranchFromHEAD(data []byte) string {
	ref := strings.TrimSpace(string(data))
	branch, ok := strings.CutPrefix(ref, "ref: refs/heads/")
	if !ok {
		return ""
	}
	return branch
}
