package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/util/fsxutil"
)

// GitIgnoredPaths returns the subset of paths that the .gitignore rules found
// in this directory ignore, in their original order. Paths are relative to the
// directory; a trailing slash marks a directory. As in git, a path beneath an
// ignored directory is ignored too. Nothing is read beyond the .gitignore
// files along each path, so this is cheap even for large trees.
func (dir *Directory) GitIgnoredPaths(ctx context.Context, self dagql.ObjectResult[*Directory], paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	snapshot, err := dir.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, nil
	}
	dirPath, err := dir.Dir.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	var ignored []string
	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, dirPath)
		if err != nil {
			return err
		}
		fsys, err := fsutil.NewFS(resolvedDir)
		if err != nil {
			return fmt.Errorf("open directory for gitignore matching: %w", err)
		}
		matcher := fsxutil.NewGitIgnoreMatcher(fsys)
		for _, p := range paths {
			isDir := strings.HasSuffix(p, "/")
			match, err := matcher.Matches(strings.TrimSuffix(p, "/"), isDir)
			if err != nil {
				return fmt.Errorf("match %q against gitignore rules: %w", p, err)
			}
			if match {
				ignored = append(ignored, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ignored, nil
}
