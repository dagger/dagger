package fsxutil

import (
	"context"
	"io"
	gofs "io/fs"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/internal/fsutil/types"
	"github.com/pkg/errors"
)

// gitignoreMarkedFS wraps an FS and annotates stats for paths excluded by gitignore.
// It does not filter entries; instead it sets Stat.GitIgnored=true when a path is
// ignored by gitignore rules.
type gitignoreMarkedFS struct {
	fs      fsutil.FS
	matcher *GitignoreMatcher
}

// NewGitIgnoreMarkedFS creates a new FS that marks gitignored paths in Stat.GitIgnored.
// The returned FS leaves filtering decisions to the consumer, but does not walk
// beneath an ignored directory unless a negation rule could re-include a path there.
func NewGitIgnoreMarkedFS(fs fsutil.FS, matcher *GitignoreMatcher) (fsutil.FS, error) {
	if matcher == nil {
		matcher = NewGitIgnoreMatcher(fs)
	}
	return &gitignoreMarkedFS{
		fs:      fs,
		matcher: matcher,
	}, nil
}

// Open implements fsutil.FS. Gitignored files are treated as missing for reads.
func (gfs *gitignoreMarkedFS) Open(path string) (io.ReadCloser, error) {
	ignored, err := gfs.matcher.Matches(path, false)
	if err != nil {
		return nil, err
	}
	if ignored {
		return nil, errors.Wrapf(os.ErrNotExist, "open %s", path)
	}
	return gfs.fs.Open(path)
}

// Walk implements fsutil.FS.
func (gfs *gitignoreMarkedFS) Walk(ctx context.Context, target string, fn gofs.WalkDirFunc) error {
	return gfs.fs.Walk(ctx, target, func(path string, dirEntry gofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry == nil {
			return fn(path, dirEntry, walkErr)
		}

		isDir := dirEntry.IsDir()
		ignored, err := gfs.matcher.Matches(path, isDir)
		if err != nil {
			return err
		}
		var skipDescendants bool
		if ignored {
			if err := markGitIgnored(dirEntry); err != nil {
				return err
			}
			if isDir {
				reincludable, err := gfs.matcher.mayReincludeBeneath(path)
				if err != nil {
					return err
				}
				skipDescendants = !reincludable
			}
		}

		err = fn(path, dirEntry, walkErr)
		if err == nil && skipDescendants {
			return filepath.SkipDir
		}
		return err
	})
}

func markGitIgnored(dirEntry gofs.DirEntry) error {
	if dirEntry == nil {
		return nil
	}
	if de, ok := dirEntry.(*fsutil.DirEntryInfo); ok {
		if de.Stat == nil {
			if _, err := de.Info(); err != nil {
				return err
			}
		}
		de.Stat.GitIgnored = true
		return nil
	}

	fi, err := dirEntry.Info()
	if err != nil {
		return err
	}
	stat, ok := fi.Sys().(*types.Stat)
	if ok {
		stat.GitIgnored = true
	}
	return nil
}
