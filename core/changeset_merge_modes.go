package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
)

type mergeFileMode struct {
	mode os.FileMode
	side int
}

func mergeFileModeCandidates(ctx context.Context, base map[string]os.FileMode, contents ...*changesetContent) (map[string][]mergeFileMode, error) {
	modes := make(map[string][]mergeFileMode, len(base))
	for p, mode := range base {
		modes[p] = []mergeFileMode{{mode: mode, side: len(contents)}}
	}
	for side, content := range contents {
		paths := content.paths.withoutGitMeta()
		if !content.hasDiff() {
			continue
		}
		err := content.withDiff(ctx, func(dir string, _ *mount.Mount) error {
			for _, p := range slices.Concat(paths.Added, paths.Modified) {
				if strings.HasSuffix(p, "/") {
					continue
				}
				info, err := lstatInRoot(dir, p)
				if err != nil {
					return TrimErrPathPrefix(err, dir)
				}
				if info.Mode().IsRegular() {
					modes[p] = append(modes[p], mergeFileMode{mode: info.Mode(), side: side})
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read merge file modes: %w", err)
		}
	}
	return modes, nil
}

// restoreFileModes restores the permissions Git cannot represent. Unchanged
// files retain the merge base's mode; changed files use their input's mode.
// When both sides change a mode, the conflict preference breaks the tie (ours
// for conflict markers and clean merges), while retaining Git's executable-bit
// decision. Symlinks and paths deleted or replaced by directories are skipped.
func (ws *gitMergeWorkspace) restoreFileModes(ctx context.Context, base map[string]os.FileMode, preferTheirs bool, branches []string, contents ...*changesetContent) error {
	modes, err := mergeFileModeCandidates(ctx, base, contents...)
	if err != nil {
		return err
	}
	base = mergeRenamedFileModes(base, modes, contents...)
	var recoveredRenames bool
	// Walking the result avoids following a symlink that replaced a base
	// directory and accidentally chmodding its target through an old child.
	return filepath.WalkDir(ws.workDir, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return TrimErrPathPrefix(err, ws.root)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := filepath.Rel(ws.workDir, full)
		if err != nil {
			return err
		}
		p = filepath.ToSlash(p)
		if gitMetaPath(p) && entry.IsDir() {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() || gitMetaPath(p) {
			return nil
		}
		if len(modes[p]) == 0 && !recoveredRenames {
			if err := ws.recoverFileModeRenames(ctx, modes, base, branches); err != nil {
				return err
			}
			recoveredRenames = true
		}
		candidates := modes[p]
		if len(candidates) == 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return TrimErrPathPrefix(err, ws.root)
		}
		return TrimErrPathPrefix(restoreMergeFileMode(full, info.Mode(), base[p], candidates, preferTheirs), ws.root)
	})
}

func (ws *gitMergeWorkspace) recoverFileModeRenames(ctx context.Context, modes map[string][]mergeFileMode, base map[string]os.FileMode, branches []string) error {
	// Directory rename detection can move an added file to a path no
	// input declared. Ask Git for that mapping only if we encounter one.
	originalModes, originalBase := maps.Clone(modes), maps.Clone(base)
	for _, branch := range branches {
		out, err := runGitOutput(ctx, ws.workDir, "diff", "--name-status", "-z", "--find-renames", "--diff-filter=R", branch, "--")
		if err != nil {
			return fmt.Errorf("find merged file renames: %w", err)
		}
		for newPath, oldPath := range parseGitOutput([]byte(out), "", "").Renamed {
			if gitMetaPath(newPath) || gitMetaPath(oldPath) || len(modes[newPath]) != 0 {
				continue
			}
			modes[newPath] = slices.Clone(originalModes[oldPath])
			if mode, ok := originalBase[oldPath]; ok {
				base[newPath] = mode
			}
		}
	}
	return nil
}

func mergeRenamedFileModes(base map[string]os.FileMode, modes map[string][]mergeFileMode, contents ...*changesetContent) map[string]os.FileMode {
	// A rename on one side can carry the other side's edits to the new path.
	// Make both paths' mode candidates available at that destination too.
	base = maps.Clone(base)
	for _, content := range contents {
		for newPath, oldPath := range content.paths.withoutGitMeta().Renamed {
			modes[newPath] = append(modes[newPath], modes[oldPath]...)
			if mode, ok := base[oldPath]; ok {
				base[newPath] = mode
			}
		}
	}
	return base
}

func restoreMergeFileMode(full string, mode, base os.FileMode, candidates []mergeFileMode, preferTheirs bool) error {
	slices.SortStableFunc(candidates, func(a, b mergeFileMode) int {
		// A changed permission wins over an unchanged base permission.
		aChanged, bChanged := a.mode != base, b.mode != base
		if aChanged != bChanged {
			if aChanged {
				return -1
			}
			return 1
		}
		if preferTheirs {
			return b.side - a.side
		}
		return a.side - b.side
	})
	for _, candidate := range candidates {
		// Git tracks only the owner's executable bit. Do not undo its
		// mode merge when the other side changed executability.
		if candidate.mode&0o100 != mode&0o100 {
			continue
		}
		if mode != candidate.mode {
			return os.Chmod(full, candidate.mode)
		}
		return nil
	}
	return nil
}

// changedFileModes reads only the base paths a native workspace merge can
// rewrite, retaining its ability to avoid walking the unchanged baseline.
func (ws *gitMergeWorkspace) changedFileModes(contents ...*changesetContent) (map[string]os.FileMode, error) {
	modes := make(map[string]os.FileMode)
	for _, content := range contents {
		paths := content.paths.withoutGitMeta()
		for _, p := range slices.Concat(paths.Added, paths.Modified, paths.AllRemoved) {
			if strings.HasSuffix(p, "/") {
				continue
			}
			info, err := lstatInRoot(ws.workDir, p)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, TrimErrPathPrefix(err, ws.root)
			}
			if info.Mode().IsRegular() {
				modes[p] = info.Mode()
			}
		}
	}
	return modes, nil
}

// restoreChangedFileModes applies the same permission merge as the general
// merge, without scanning untouched files or requiring its temporary Git repo.
func (ws *gitMergeWorkspace) restoreChangedFileModes(ctx context.Context, base map[string]os.FileMode, contents ...*changesetContent) error {
	modes, err := mergeFileModeCandidates(ctx, base, contents...)
	if err != nil {
		return err
	}
	base = mergeRenamedFileModes(base, modes, contents...)
	for p, candidates := range modes {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := lstatInRoot(ws.workDir, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return TrimErrPathPrefix(err, ws.root)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		full := filepath.Join(ws.workDir, filepath.FromSlash(p))
		if err := restoreMergeFileMode(full, info.Mode(), base[p], candidates, false); err != nil {
			return TrimErrPathPrefix(err, ws.root)
		}
	}
	return nil
}
