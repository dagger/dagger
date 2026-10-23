package fsxutil

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/pkg/errors"
)

type GitignoreMatcher struct {
	fs fsutil.FS
	// Overrides replace rule files without changing the filesystem. An empty
	// value also represents a removed rule file. Keys are slash-relative paths.
	overrides    map[string]string
	explicitDirs []string

	// Cache for parsed gitignore files to avoid re-reading
	gitignoreCache         map[string]gitignore.Matcher
	gitignoreCachePatterns map[string][]gitignore.Pattern
	gitignoreCacheMu       sync.RWMutex
}

// NewGitIgnoreMatcher creates a new GitignoreMatcher for the given FS
func NewGitIgnoreMatcher(fs fsutil.FS) *GitignoreMatcher {
	gfs := &GitignoreMatcher{
		fs:                     fs,
		gitignoreCache:         make(map[string]gitignore.Matcher),
		gitignoreCachePatterns: make(map[string][]gitignore.Pattern),
	}
	return gfs
}

func NewGitIgnoreMatcherWithOverrides(fs fsutil.FS, overrides map[string]string, explicitDirs ...string) *GitignoreMatcher {
	matcher := NewGitIgnoreMatcher(fs)
	matcher.overrides = overrides
	matcher.explicitDirs = explicitDirs
	return matcher
}

// Matches checks if a path should be ignored based on gitignore rules
func (gfs *GitignoreMatcher) Matches(path string, isDir bool) (out bool, _ error) {
	// Clean the path and ensure it's relative
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		path = strings.TrimPrefix(path, "/")
	}

	// Get the directory containing this path
	var dirPath string
	if isDir {
		dirPath = path
	} else {
		dirPath = filepath.Dir(path)
		if dirPath == "." && path != "." {
			dirPath = ""
		}
	}

	// Get all accumulated patterns for this directory
	matcher, err := gfs.getGitIgnoreMatcher(dirPath)
	if err != nil {
		return false, err
	}
	if matcher == nil {
		// No patterns found, nothing to ignore
		return false, nil
	}

	pathComponents := strings.Split(path, string(filepath.Separator))
	return matcher.Match(pathComponents, isDir), nil
}

func (gfs *GitignoreMatcher) getGitIgnoreMatcher(dirPath string) (matcher gitignore.Matcher, rerr error) {
	if dirPath == "" {
		dirPath = "."
	}

	gfs.gitignoreCacheMu.RLock()
	if matcher, exists := gfs.gitignoreCache[dirPath]; exists {
		gfs.gitignoreCacheMu.RUnlock()
		return matcher, nil
	}
	gfs.gitignoreCacheMu.RUnlock()

	defer func() {
		if rerr == nil {
			gfs.gitignoreCacheMu.Lock()
			gfs.gitignoreCache[dirPath] = matcher
			gfs.gitignoreCacheMu.Unlock()
		}
	}()

	patterns, err := gfs.getGitIgnorePatterns(dirPath)
	if err != nil {
		return nil, err
	}
	if len(patterns) > 0 {
		// Selecting a directory bypasses matches at or above that directory,
		// but preserves fresh matches below it. Choose the deepest
		// selected ancestor so nested explicit references can cross barriers.
		var selected string
		for _, dir := range gfs.explicitDirs {
			dir = filepath.FromSlash(dir)
			if dir == "." || dir == "" {
				continue
			}
			if (dirPath == dir || strings.HasPrefix(dirPath, dir+string(filepath.Separator))) && len(dir) > len(selected) {
				selected = dir
			}
		}
		if selected != "" {
			components := strings.Split(selected, string(filepath.Separator))
			patterns = slices.Clone(patterns)
			for i, pattern := range patterns {
				if pattern.Match(components, true) == gitignore.Exclude {
					rule := *pattern.(*gitignoreRule)
					rule.explicitDepth = len(components)
					patterns[i] = &rule
				}
			}
		}
		matcher = gitignore.NewMatcher(patterns)
	}
	return matcher, nil
}

func (gfs *GitignoreMatcher) getGitIgnorePatterns(dirPath string) (patterns []gitignore.Pattern, rerr error) {
	if dirPath == "" {
		dirPath = "."
	}

	gfs.gitignoreCacheMu.RLock()
	if patterns, exists := gfs.gitignoreCachePatterns[dirPath]; exists {
		gfs.gitignoreCacheMu.RUnlock()
		return patterns, nil
	}
	gfs.gitignoreCacheMu.RUnlock()

	defer func() {
		if rerr == nil {
			gfs.gitignoreCacheMu.Lock()
			gfs.gitignoreCachePatterns[dirPath] = patterns
			gfs.gitignoreCacheMu.Unlock()
		}
	}()

	if dirPath != "." {
		parentDir := filepath.Dir(dirPath)
		var err error
		patterns, err = gfs.getGitIgnorePatterns(parentDir)
		if err != nil {
			return nil, err
		}
	}

	gitignorePath := filepath.Join(dirPath, ".gitignore")
	var reader io.ReadCloser
	var err error
	if contents, ok := gfs.overrides[filepath.ToSlash(gitignorePath)]; ok {
		reader = io.NopCloser(strings.NewReader(contents))
	} else {
		reader, err = gfs.fs.Open(gitignorePath)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return patterns, nil
		}
		return nil, err
	}
	defer reader.Close()

	// Parse the .gitignore file
	domain := strings.Split(dirPath, string(filepath.Separator))
	if dirPath == "." {
		domain = nil
	}

	// Read patterns from the .gitignore filepath
	newPatterns, err := parseGitIgnoreFile(reader, domain)
	if err != nil {
		return nil, err
	}
	patterns = slices.Clone(patterns)
	patterns = append(patterns, newPatterns...)

	return patterns, nil
}

// parseGitIgnoreFile parses a gitignore file and returns patterns
func parseGitIgnoreFile(reader io.Reader, domain []string) ([]gitignore.Pattern, error) {
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			// skip empty lines and comments
			continue
		}
		glob := strings.TrimPrefix(line, "!")
		if !strings.HasSuffix(glob, `\ `) {
			glob = strings.TrimRight(glob, " ")
		}
		dirOnly := strings.HasSuffix(glob, "/")
		glob = strings.TrimSuffix(glob, "/")
		if !strings.Contains(glob, "/") {
			glob = "**/" + glob
		}
		patterns = append(patterns, &gitignoreRule{
			Pattern:     gitignore.ParsePattern(line, domain),
			glob:        strings.TrimPrefix(glob, "/"),
			domainDepth: len(domain),
			dirOnly:     dirOnly,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}

// gitignoreRule retains an exact glob as well as Git's ancestor-aware matcher.
// An explicit root bypasses ancestor matches, so we must distinguish those
// from new matches of the same rule against descendants of that root.
type gitignoreRule struct {
	gitignore.Pattern
	glob          string
	domainDepth   int
	dirOnly       bool
	explicitDepth int
}

func (rule *gitignoreRule) Match(path []string, isDir bool) gitignore.MatchResult {
	match := rule.Pattern.Match(path, isDir)
	if rule.explicitDepth == 0 || match != gitignore.Exclude {
		return match
	}
	for end := rule.explicitDepth + 1; end <= len(path); end++ {
		if rule.dirOnly && end == len(path) && !isDir {
			continue
		}
		if matched, _ := doublestar.Match(rule.glob, strings.Join(path[rule.domainDepth:end], "/")); matched {
			return gitignore.Exclude
		}
	}
	return gitignore.NoMatch
}
