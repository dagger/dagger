package fsxutil

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
)

type GitignoreMatcher struct {
	fs fsutil.FS

	// Cache for parsed gitignore files to avoid re-reading
	gitignoreCache         map[string]gitignore.Matcher
	gitignoreCachePatterns map[string][]gitignore.Pattern
	gitignoreCacheMu       sync.RWMutex
}

// NewGitignoreMatcher creates a new GitignoreMatcher for the given FS
func NewGitIgnoreMatcher(fs fsutil.FS) *GitignoreMatcher {
	gfs := &GitignoreMatcher{
		fs:                     fs,
		gitignoreCache:         make(map[string]gitignore.Matcher),
		gitignoreCachePatterns: make(map[string][]gitignore.Pattern),
	}
	return gfs
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
	reader, err := gfs.fs.Open(gitignorePath)
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
		pattern := gitignore.ParsePattern(line, domain)
		patterns = append(patterns, pattern)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}
