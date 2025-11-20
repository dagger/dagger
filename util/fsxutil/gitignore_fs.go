package fsxutil

import (
	"context"
	"io"
	gofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/pkg/errors"
)

// gitignoreFS wraps an FS and filters files based on .gitignore rules
type gitignoreFS struct {
	fs      fsutil.FS
	matcher *GitignoreMatcher
}

// NewGitIgnoreFS creates a new FS that filters the given FS using gitignore rules
func NewGitIgnoreFS(fs fsutil.FS, matcher *GitignoreMatcher) (fsutil.FS, error) {
	if matcher == nil {
		matcher = NewGitIgnoreMatcher(fs)
	}
	gfs := &gitignoreFS{
		fs:      fs,
		matcher: matcher,
	}
	return gfs, nil
}

// Open implements fsutil.FS
func (gfs *gitignoreFS) Open(path string) (io.ReadCloser, error) {
	ignored, err := gfs.matcher.Matches(path, false)
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
		ignored, err := gfs.matcher.Matches(path, isDir)
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
