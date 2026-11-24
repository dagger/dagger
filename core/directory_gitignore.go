package core

import (
	"context"
	"fmt"
	"path"
	"slices"
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

// WithoutGitIgnoredAdditions removes from after every path that changes reports
// as added and the after tree's .gitignore rules ignore, mirroring git: ignore
// rules only ever hide untracked files, so modifications and deletions of
// tracked paths are kept even when those paths match a rule. Directories left
// empty by the removals, and that only exist because of them, are removed too,
// since git never reports an empty directory. Returns the number of ignored
// additions removed; zero means after is returned untouched.
func WithoutGitIgnoredAdditions(
	ctx context.Context,
	srv *dagql.Server,
	after dagql.ObjectResult[*Directory],
	changes dagql.ObjectResult[*Changeset],
) (dagql.ObjectResult[*Directory], int, error) {
	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return after, 0, err
	}
	ignored, err := after.Self().GitIgnoredPaths(ctx, after, paths.Added)
	if err != nil {
		return after, 0, err
	}
	if len(ignored) == 0 {
		return after, 0, nil
	}
	dirs, files, parents := partitionIgnoredAdditions(paths.Added, ignored)
	for _, d := range dirs {
		if err := srv.Select(ctx, after, &after, dagql.Selector{Field: "withoutDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(d)},
		}}); err != nil {
			return after, 0, err
		}
	}
	if len(files) > 0 {
		if err := srv.Select(ctx, after, &after, dagql.Selector{Field: "withoutFiles", Args: []dagql.NamedInput{
			{Name: "paths", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(files...))},
		}}); err != nil {
			return after, 0, err
		}
	}
	// Prune newly added directories that held nothing but ignored files,
	// deepest first so a chain of them collapses.
	for _, dir := range parents {
		entries, err := after.Self().Entries(ctx, after, dir)
		if err != nil {
			return after, 0, err
		}
		if len(entries) > 0 {
			continue
		}
		if err := srv.Select(ctx, after, &after, dagql.Selector{Field: "withoutDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(dir)},
		}}); err != nil {
			return after, 0, err
		}
	}
	return after, len(ignored), nil
}

// partitionIgnoredAdditions plans the removal of ignored additions from a
// tree. Ignored directories are added wholesale (a directory in added does not
// exist in the baseline), so dropping the outermost ones covers their
// contents: dirs lists those, without their trailing slash. files lists every
// other ignored path. parents lists the added directories containing those
// files, deepest first, as candidates to prune once they turn out empty.
func partitionIgnoredAdditions(added, ignored []string) (dirs, files, parents []string) {
	addedSet := make(map[string]struct{}, len(added))
	for _, p := range added {
		addedSet[p] = struct{}{}
	}
	var ignoredDirs []string
	for _, p := range ignored {
		if strings.HasSuffix(p, "/") {
			ignoredDirs = append(ignoredDirs, p)
		}
	}
	slices.Sort(ignoredDirs)
	var outermost []string
	for _, d := range ignoredDirs {
		if !underAnyDir(d, outermost) {
			outermost = append(outermost, d)
		}
	}
	for _, p := range ignored {
		if !strings.HasSuffix(p, "/") && !underAnyDir(p, outermost) {
			files = append(files, p)
		}
	}
	for _, d := range outermost {
		dirs = append(dirs, strings.TrimSuffix(d, "/"))
	}
	seen := map[string]struct{}{}
	for _, f := range files {
		for dir := path.Dir(f); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := seen[dir]; ok {
				break
			}
			seen[dir] = struct{}{}
			if _, ok := addedSet[dir+"/"]; ok {
				parents = append(parents, dir)
			}
		}
	}
	slices.SortStableFunc(parents, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	return dirs, files, parents
}

// underAnyDir reports whether p lies beneath one of dirs (each ending in "/").
func underAnyDir(p string, dirs []string) bool {
	for _, d := range dirs {
		if p != d && strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}
