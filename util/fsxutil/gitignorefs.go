package fsxutil

import (
	"bufio"
	"context"
	"io"
	gofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
)

// gitignoreFS wraps an FS and filters files based on .gitignore rules
type gitignoreFS struct {
	fs fsutil.FS

	// Cache for parsed gitignore files to avoid re-reading
	gitignoreCache         map[string]gitignore.Matcher
	gitignoreCachePatterns map[string][]gitignore.Pattern
	gitignoreCacheMu       sync.RWMutex
}

// NewGitIgnoreFS creates a new FS that filters the given FS using gitignore rules
func NewGitIgnoreFS(fs fsutil.FS) (fsutil.FS, error) {
	gfs := &gitignoreFS{
		fs:                     fs,
		gitignoreCache:         make(map[string]gitignore.Matcher),
		gitignoreCachePatterns: make(map[string][]gitignore.Pattern),
	}
	return gfs, nil
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

// getGitIgnoreMatcher returns a gitignore matcher for the given directory
func (gfs *gitignoreFS) getGitIgnoreMatcher(dirPath string) (matcher gitignore.Matcher, rerr error) {
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

func (gfs *gitignoreFS) getGitIgnorePatterns(dirPath string) (patterns []gitignore.Pattern, rerr error) {
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

// isIgnored checks if a path should be ignored based on gitignore rules
func (gfs *gitignoreFS) isIgnored(path string, isDir bool) (out bool, _ error) {
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

// Open implements fsutil.FS
func (gfs *gitignoreFS) Open(path string) (io.ReadCloser, error) {
	ignored, err := gfs.isIgnored(path, false)
	if err != nil {
		return nil, err
	}
	if ignored {
		return nil, errors.Wrapf(os.ErrNotExist, "open %s", path)
	}
	return gfs.fs.Open(path)
}

// Walk implements fsutil.FS
func (gfs *gitignoreFS) Walk(ctx context.Context, target string, fn gofs.WalkDirFunc) error {
	type visitedDir struct {
		entry       gofs.DirEntry
		pathWithSep string
	}
	var parentDirs []visitedDir

	return gfs.fs.Walk(ctx, target, func(path string, dirEntry gofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		for len(parentDirs) != 0 {
			lastParentDir := parentDirs[len(parentDirs)-1].pathWithSep
			if strings.HasPrefix(path, lastParentDir) {
				break
			}
			parentDirs = parentDirs[:len(parentDirs)-1]
		}

		isDir := dirEntry != nil && dirEntry.IsDir()

		// Check if this path should be ignored
		ignored, err := gfs.isIgnored(path, isDir)
		if err != nil {
			return err
		}

		if ignored {
			if isDir {
				dir := visitedDir{
					entry:       dirEntry,
					pathWithSep: path + string(filepath.Separator),
				}
				parentDirs = append(parentDirs, dir)

				// Skip the entire directory
				// return filepath.SkipDir
				return nil
			}
			// Skip this file
			return nil
		}

		for _, dir := range slices.Backward(parentDirs) {
			if err := fn(strings.TrimSuffix(dir.pathWithSep, string(filepath.Separator)), dir.entry, nil); err != nil {
				return err
			}
		}
		return fn(path, dirEntry, nil)
	})
}
