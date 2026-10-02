package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"

	"github.com/dagger/dagger/dagql"
)

// PatchRebasePlan is how to finish a changeset's patch once applied to a base
// other than its Before: the directory edits a git patch cannot express.
type PatchRebasePlan struct {
	// MkDirs are directories the changeset leaves in place that the patch may
	// not: empty directories it adds, and parents a deletion may prune. Parents
	// come before children.
	MkDirs []PatchRebaseDir
	// RmDirs are directories the changeset removes.
	RmDirs []string
}

type PatchRebaseDir struct {
	Path        string
	Permissions int
}

// PlanPatchRebase reports whether ch's patch, applied to base, gives the same
// files as applying ch itself to base (Directory.withChanges), and if so how to
// reconcile directories afterwards. That holds when base agrees with Before at
// every path ch changes: the patch reproduces After there, and withChanges
// copies exactly After's content there. It is checked by reading only those
// paths (and the subtrees of removed directories) from mounts of the three
// trees, with no snapshots made. ok is false when base differs from Before.
func (ch *Changeset) PlanPatchRebase(ctx context.Context, base dagql.ObjectResult[*Directory]) (_ *PatchRebasePlan, ok bool, _ error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("compute changeset paths: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := cache.Evaluate(ctx, ch.Before, ch.After, base); err != nil {
		return nil, false, err
	}
	var plan *PatchRebasePlan
	err = withReadOnlyDirectory(ctx, ch.Before, func(beforeRoot string) error {
		return withReadOnlyDirectory(ctx, ch.After, func(afterRoot string) error {
			return withReadOnlyDirectory(ctx, base, func(baseRoot string) error {
				var err error
				plan, ok, err = planPatchRebase(paths, beforeRoot, afterRoot, baseRoot)
				return err
			})
		})
	})
	return plan, ok, err
}

func planPatchRebase(paths *ChangesetPaths, beforeRoot, afterRoot, baseRoot string) (*PatchRebasePlan, bool, error) {
	for _, p := range slices.Concat(paths.Added, paths.Modified, paths.AllRemoved) {
		same, err := sameEntry(beforeRoot, baseRoot, strings.TrimSuffix(p, "/"))
		if err != nil || !same {
			return nil, false, err
		}
	}
	plan := &PatchRebasePlan{}
	for _, p := range paths.Removed {
		if !strings.HasSuffix(p, "/") {
			continue
		}
		dir := strings.TrimSuffix(p, "/")
		plan.RmDirs = append(plan.RmDirs, dir)
		// withChanges removes the whole directory; the patch only the files
		// Before had there.
		extra := false
		err := filepath.WalkDir(filepath.Join(baseRoot, dir), func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(baseRoot, p)
			if err != nil {
				return err
			}
			if _, err := os.Lstat(filepath.Join(beforeRoot, rel)); err != nil {
				extra = true
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
		if extra {
			return nil, false, nil
		}
	}
	underRemoved := func(p string) bool {
		return slices.ContainsFunc(plan.RmDirs, func(r string) bool {
			return p == r || strings.HasPrefix(p, r+"/")
		})
	}
	// The patch creates the directories its added files live in; only those
	// left empty need creating.
	filled := map[string]bool{}
	for _, p := range paths.Added {
		if strings.HasSuffix(p, "/") {
			continue
		}
		for dir := path.Dir(p); dir != "." && dir != "/" && !filled[dir]; dir = path.Dir(dir) {
			filled[dir] = true
		}
	}
	mk := map[string]int{}
	for _, p := range paths.Added {
		if !strings.HasSuffix(p, "/") {
			continue
		}
		dir := strings.TrimSuffix(p, "/")
		if filled[dir] {
			continue
		}
		fi, err := os.Lstat(filepath.Join(afterRoot, dir))
		if err != nil {
			return nil, false, err
		}
		mk[dir] = int(fi.Mode().Perm())
	}
	for _, p := range paths.AllRemoved {
		if strings.HasSuffix(p, "/") {
			continue
		}
		for parent := path.Dir(p); parent != "." && parent != "/"; parent = path.Dir(parent) {
			if underRemoved(parent) {
				break
			}
			if _, ok := mk[parent]; ok {
				continue
			}
			fi, err := os.Lstat(filepath.Join(baseRoot, parent))
			if err != nil || !fi.IsDir() {
				continue
			}
			mk[parent] = int(fi.Mode().Perm())
		}
	}
	for dir, perms := range mk {
		plan.MkDirs = append(plan.MkDirs, PatchRebaseDir{Path: dir, Permissions: perms})
	}
	slices.SortFunc(plan.MkDirs, func(a, b PatchRebaseDir) int { return strings.Compare(a.Path, b.Path) })
	return plan, true, nil
}

// sameEntry reports whether rel is the same in both trees as far as a git
// patch can tell: both absent, both directories, or files with the same
// git-visible mode and content.
func sameEntry(aRoot, bRoot, rel string) (bool, error) {
	aFi, aErr := os.Lstat(filepath.Join(aRoot, rel))
	bFi, bErr := os.Lstat(filepath.Join(bRoot, rel))
	aMissing := errors.Is(aErr, fs.ErrNotExist) || errors.Is(aErr, syscall.ENOTDIR)
	bMissing := errors.Is(bErr, fs.ErrNotExist) || errors.Is(bErr, syscall.ENOTDIR)
	switch {
	case aErr != nil && !aMissing:
		return false, aErr
	case bErr != nil && !bMissing:
		return false, bErr
	case aMissing || bMissing:
		return aMissing && bMissing, nil
	case aFi.IsDir() || bFi.IsDir():
		return aFi.IsDir() && bFi.IsDir(), nil
	}
	differs, err := modifiedFileDiffers(aRoot, bRoot, rel)
	return !differs, err
}

// withReadOnlyDirectory mounts an evaluated Directory read-only and calls fn
// with the path of its root.
func withReadOnlyDirectory(ctx context.Context, dir dagql.ObjectResult[*Directory], fn func(root string) error) error {
	ref, err := dir.Self().Snapshot.GetOrEval(ctx, dir.Result)
	if err != nil {
		return err
	}
	selector, err := dir.Self().Dir.GetOrEval(ctx, dir.Result)
	if err != nil {
		return err
	}
	if ref == nil {
		empty, err := os.MkdirTemp("", "dagger-empty-dir-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(empty)
		return fn(empty)
	}
	return MountRef(ctx, ref, func(m string, _ *mount.Mount) error {
		root, err := containerdfs.RootPath(m, selector)
		if err != nil {
			return err
		}
		return fn(root)
	}, mountRefAsReadOnly)
}
