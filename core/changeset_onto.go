package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// PatchOnto is a changeset rendered against a tree other than its own Before:
// applying Patch to that tree with `git apply`, then creating NewDirectories
// and removing RemovedDirectories, leaves every path the changeset declares as
// Directory.withChanges would, without comparing the two trees.
type PatchOnto struct {
	// Patch is a `git diff --binary` from the tree's content at the
	// changeset's paths to the changeset's After content there. Empty when
	// they already match.
	Patch []byte
	// NewDirectories are the directories the result must have that a patch
	// cannot express: directories the changeset adds, and those of After's
	// that `git apply` prunes once it deletes their last file. Parents come
	// before their children.
	NewDirectories []PatchOntoDirectory
	// RemovedDirectories are the directories the changeset removes. Their
	// files are deleted by Patch already; this removes what remains, e.g.
	// empty subdirectories.
	RemovedDirectories []string
}

// PatchOntoDirectory is a directory to create, with its mode in After.
type PatchOntoDirectory struct {
	Path        string
	Permissions int
}

// IsEmpty reports whether applying p changes nothing.
func (p *PatchOnto) IsEmpty() bool {
	return len(p.Patch) == 0 && len(p.NewDirectories) == 0 && len(p.RemovedDirectories) == 0
}

// ErrPatchTooLarge is returned by RenderPatchOnto when the patch exceeds its
// size budget.
var ErrPatchTooLarge = errors.New("patch exceeds the size budget")

// RenderPatchOnto renders the changeset as a patch against base, a tree the
// changeset is about to be applied to at prefix (a base-relative directory;
// "." for its root). Paths in the result are base-relative.
//
// Only the changeset's paths are staged — base's content there, and After's —
// and `git diff` runs between the two, so the cost follows the size of the
// change rather than of either tree. Because the patch starts from base's own
// content, applying it reproduces the changeset by construction, even where
// base and Before differ: a file the changeset adds that base already has
// becomes a modification, and a file it removes that base never had drops out.
//
// The patch is read into memory; past maxBytes it fails with ErrPatchTooLarge.
func (ch *Changeset) RenderPatchOnto(ctx context.Context, base dagql.ObjectResult[*Directory], prefix string, maxBytes int64) (*PatchOnto, error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute changeset paths: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, base, ch.After); err != nil {
		return nil, fmt.Errorf("evaluate patch trees: %w", err)
	}
	var out *PatchOnto
	err = mountDirectoryOrEmpty(ctx, base, func(baseDir string) error {
		return mountDirectoryOrEmpty(ctx, ch.After, func(afterDir string) (err error) {
			out, err = renderPatchOntoDirs(ctx, baseDir, afterDir, prefix, paths, maxBytes)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// renderPatchOntoDirs is RenderPatchOnto over mounted trees, given the
// changeset's paths.
func renderPatchOntoDirs(ctx context.Context, baseDir, afterDir, prefix string, paths *ChangesetPaths, maxBytes int64) (*PatchOnto, error) {
	prefix = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(prefix)), "/")
	if prefix == "" {
		prefix = "."
	}
	rooted := func(p string) string {
		return path.Join(prefix, strings.TrimSuffix(p, "/"))
	}

	stage, err := os.MkdirTemp("", "dagger-patch-onto-")
	if err != nil {
		return nil, fmt.Errorf("create patch staging dir: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedBase := filepath.Join(stage, "a")
	stagedAfter := filepath.Join(stage, "b")
	for _, dir := range []string{stagedBase, stagedAfter} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return nil, err
		}
	}

	var baseFiles, afterFiles []string
	for _, p := range slices.Concat(paths.Added, paths.Modified) {
		if strings.HasSuffix(p, "/") {
			continue
		}
		afterFiles = append(afterFiles, p)
		// Whatever base holds there is replaced, including a directory
		// where After has a file.
		found, err := treeFiles(baseDir, rooted(p))
		if err != nil {
			return nil, err
		}
		baseFiles = append(baseFiles, found...)
	}
	for _, p := range paths.Removed {
		found, err := treeFiles(baseDir, rooted(p))
		if err != nil {
			return nil, err
		}
		baseFiles = append(baseFiles, found...)
	}
	slices.Sort(baseFiles)
	baseFiles = slices.Compact(baseFiles)

	if err := materializeDeltaFiles(ctx, baseDir, stagedBase, baseFiles); err != nil {
		return nil, fmt.Errorf("stage base files: %w", err)
	}
	// After is rooted at prefix within base: stage its files there, so the
	// patch's paths are base-relative.
	if err := materializeDeltaFiles(ctx, afterDir, filepath.Join(stagedAfter, prefix), afterFiles); err != nil {
		return nil, fmt.Errorf("stage after files: %w", err)
	}

	out := &PatchOnto{}
	if len(baseFiles) > 0 || len(afterFiles) > 0 {
		var patch bytes.Buffer
		var stderr strings.Builder
		budget := &patchBudgetWriter{w: &patch, remaining: maxBytes}
		if err := writeGitDiffPatch(ctx, stage, nil, budget, io.Discard, &stderr); err != nil {
			if budget.exceeded {
				return nil, ErrPatchTooLarge
			}
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		out.Patch = patch.Bytes()
	}

	out.NewDirectories, err = patchOntoNewDirectories(baseDir, afterDir, prefix, paths, baseFiles, afterFiles)
	if err != nil {
		return nil, err
	}
	for _, p := range paths.Removed {
		if !strings.HasSuffix(p, "/") {
			continue
		}
		// A directory replaced by a file is removed by the patch; removing
		// the path afterwards would take the file too.
		if _, err := lstatInRoot(afterDir, p); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		out.RemovedDirectories = append(out.RemovedDirectories, rooted(p))
	}
	return out, nil
}

// patchOntoNewDirectories lists the directories a patch cannot leave as the
// changeset would, with their modes: the directories the changeset adds, and
// those `git apply` prunes once it deletes their last file but the changeset
// keeps. Paths are base-relative; After is rooted at prefix.
func patchOntoNewDirectories(baseDir, afterDir, prefix string, paths *ChangesetPaths, baseFiles, afterFiles []string) ([]PatchOntoDirectory, error) {
	rooted := func(p string) string {
		return path.Join(prefix, strings.TrimSuffix(p, "/"))
	}
	dirs := map[string]int{}
	for _, p := range paths.Added {
		if !strings.HasSuffix(p, "/") {
			continue
		}
		fi, err := lstatInRoot(afterDir, p)
		if err != nil {
			return nil, fmt.Errorf("stat added directory %q: %w", p, err)
		}
		dirs[rooted(p)] = int(fi.Mode().Perm())
	}

	// The files the patch deletes: base has them, After has no file there.
	written := make(map[string]bool, len(afterFiles))
	for _, p := range afterFiles {
		written[rooted(p)] = true
	}
	deleted := map[string]bool{}
	for _, p := range baseFiles {
		if !written[p] {
			deleted[p] = true
		}
	}
	// emptied reports whether deleting those files leaves nothing under dir,
	// so `git apply` removes it.
	emptiedMemo := map[string]bool{}
	var emptied func(dir string) (bool, error)
	emptied = func(dir string) (bool, error) {
		if v, ok := emptiedMemo[dir]; ok {
			return v, nil
		}
		entries, err := os.ReadDir(filepath.Join(baseDir, dir))
		if err != nil {
			return false, err
		}
		result := true
		for _, ent := range entries {
			rel := path.Join(dir, ent.Name())
			if ent.IsDir() {
				sub, err := emptied(rel)
				if err != nil {
					return false, err
				}
				if !sub {
					result = false
					break
				}
			} else if !deleted[rel] {
				result = false
				break
			}
		}
		emptiedMemo[dir] = result
		return result, nil
	}

	parents := map[string]struct{}{}
	for p := range deleted {
		for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
			parents[dir] = struct{}{}
		}
	}
	for dir := range parents {
		if _, ok := dirs[dir]; ok {
			continue
		}
		gone, err := emptied(dir)
		if err != nil {
			return nil, fmt.Errorf("inspect directory %q: %w", dir, err)
		}
		if !gone {
			continue
		}
		var fi fs.FileInfo
		if rel, inside := strings.CutPrefix(dir, prefix+"/"); inside || prefix == "." {
			if prefix == "." {
				rel = dir
			}
			// The changeset keeps it only if After has it.
			fi, err = lstatInRoot(afterDir, rel)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
		} else {
			// prefix itself or one of its parents: outside the changeset.
			fi, err = lstatInRoot(baseDir, dir)
		}
		if err != nil {
			return nil, fmt.Errorf("stat directory %q: %w", dir, err)
		}
		if fi.IsDir() {
			dirs[dir] = int(fi.Mode().Perm())
		}
	}

	out := make([]PatchOntoDirectory, 0, len(dirs))
	for dir, perm := range dirs {
		out = append(out, PatchOntoDirectory{Path: dir, Permissions: perm})
	}
	// Parents sort before their children.
	slices.SortFunc(out, func(a, b PatchOntoDirectory) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// treeFiles returns rel itself if it is a file or symlink under root, every
// file and symlink beneath it if it is a directory, and nothing if it does not
// exist.
func treeFiles(root, rel string) ([]string, error) {
	fi, err := lstatInRoot(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{rel}, nil
	}
	var files []string
	err = filepath.WalkDir(filepath.Join(root, rel), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		sub, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(sub))
		return nil
	})
	return files, err
}

// lstatInRoot is os.Lstat of rel beneath root, refusing to resolve it through
// a parent that is not a directory: a symlink there could lead out of root. A
// path whose parent is a symlink or a file does not exist in the tree, so it
// reports fs.ErrNotExist.
func lstatInRoot(root, rel string) (fs.FileInfo, error) {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." {
		return os.Lstat(root)
	}
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	cur := root
	for i, part := range parts {
		if part == ".." {
			return nil, fmt.Errorf("path %q escapes its root", rel)
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 {
			return fi, nil
		}
		if !fi.IsDir() {
			return nil, fs.ErrNotExist
		}
	}
	return nil, fs.ErrNotExist
}

// mountDirectoryOrEmpty mounts dir read-only, or an empty directory when it
// has no snapshot (an empty directory result).
func mountDirectoryOrEmpty(ctx context.Context, dir dagql.ObjectResult[*Directory], fn func(string) error) error {
	err := dir.Self().Mount(ctx, dir, fn)
	if !errors.Is(err, errEmptyResultRef) {
		return err
	}
	empty, err := os.MkdirTemp("", "dagger-empty-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(empty)
	return fn(empty)
}

// patchBudgetWriter fails writes past its budget, recording that it did.
type patchBudgetWriter struct {
	w         io.Writer
	remaining int64
	exceeded  bool
}

func (l *patchBudgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.remaining {
		l.exceeded = true
		return 0, ErrPatchTooLarge
	}
	l.remaining -= int64(len(p))
	return l.w.Write(p)
}
