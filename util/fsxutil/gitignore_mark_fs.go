package fsxutil

import (
	"context"
	"io"
	gofs "io/fs"
	"os"

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
// The returned FS still walks the full tree (to preserve negation semantics), but it
// leaves filtering decisions to the consumer.
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
		if ignored {
			if err := markGitIgnored(dirEntry); err != nil {
				return err
			}
		}

		return fn(path, dirEntry, walkErr)
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
