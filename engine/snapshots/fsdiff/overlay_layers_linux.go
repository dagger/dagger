//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	continuityfs "github.com/containerd/continuity/fs"
	pkgerrors "github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// LayerDelta is what separates two overlay snapshots of one lineage: the
// layers each has above the layers they share, bottom first. Every
// difference between the two trees is recorded in those layers: on top of
// the same shared layers, a path can only differ where one side's own
// layers hold an entry for it.
type LayerDelta struct {
	// Lower are the layers only the lower snapshot has.
	Lower []string
	// Upper are the layers only the upper snapshot has.
	Upper []string
	// Same reports that both snapshots have exactly the same layers: their
	// trees are identical.
	Same bool
}

// Empty reports whether there is nothing to go by: no layers to walk, nor
// the knowledge that there are none because both sides are the same.
func (d *LayerDelta) Empty() bool {
	return d == nil || !d.Same && len(d.Lower) == 0 && len(d.Upper) == 0
}

// GetLayerDelta returns the layers separating lower from upper, two
// snapshots that share their bottom layers: upper one or more operations
// above lower (GetUpperdir's case, generalized to several layers), or both
// built on a common ancestor, as two copy-on-write children of one snapshot
// are. It fails when they share no layer, or nothing separates them.
func GetLayerDelta(lower, upper []mount.Mount) (LayerDelta, error) {
	if len(lower) != 1 || len(upper) != 1 {
		upperdir, err := GetUpperdir(lower, upper)
		if err != nil {
			return LayerDelta{}, err
		}
		return LayerDelta{Upper: []string{upperdir}}, nil
	}
	layers := func(m mount.Mount) ([]string, error) {
		switch {
		case m.Type == "bind":
			return []string{m.Source}, nil
		case IsOverlayMountType(m):
			return GetOverlayLayers(m)
		default:
			return nil, pkgerrors.Errorf("cannot get layer information from mount option (type = %q)", m.Type)
		}
	}
	lowerlayers, err := layers(lower[0])
	if err != nil {
		return LayerDelta{}, err
	}
	upperlayers, err := layers(upper[0])
	if err != nil {
		return LayerDelta{}, err
	}
	common := 0
	for common < len(lowerlayers) && common < len(upperlayers) && lowerlayers[common] == upperlayers[common] {
		common++
	}
	if common == 0 {
		return LayerDelta{}, pkgerrors.Errorf("snapshots share no layers (%s of %d layers, %s of %d)", lower[0].Type, len(lowerlayers), upper[0].Type, len(upperlayers))
	}
	if common == len(lowerlayers) && common == len(upperlayers) {
		// Two references to one snapshot's layers, as two recipes for one
		// result may be.
		return LayerDelta{Same: true}, nil
	}
	delta := LayerDelta{Lower: lowerlayers[common:], Upper: upperlayers[common:]}
	if len(delta.Lower) == 0 {
		delta.Lower = nil
	}
	if len(delta.Upper) == 0 {
		delta.Upper = nil
	}
	if slices.Contains(delta.Lower, "") || slices.Contains(delta.Upper, "") {
		return LayerDelta{}, pkgerrors.Errorf("cannot determine layers from mount option")
	}
	return delta, nil
}

// WalkLayerDeltaChanges reports the changes from lowerView to upperView,
// the merged views of two snapshots that delta separates, in the order and
// with the semantics of a double walk of the two views (WalkChanges), but
// costing the size of delta's layers. A single upper layer above lower is
// WalkUpperdirChanges itself.
//
// Every path in any of delta's layers is a candidate; each is classified by
// comparing the two merged views, so content a later layer reverts, or a
// whiteout a later layer refills, is no change. A deleted directory is
// reported once, without its contents, as is a directory replaced by a file.
// A directory opaque in any of the layers, or replaced in one (by a whiteout
// or a file) and made a directory again by a later one, hides the shared
// layers beneath it on one side, so it is diffed in full. A redirected
// directory is an error: the caller falls back to WalkChanges.
func WalkLayerDeltaChanges(
	ctx context.Context,
	changeFn continuityfs.ChangeFunc,
	delta LayerDelta,
	upperView, lowerView string,
	comparison Comparison,
) error {
	if delta.Empty() {
		return pkgerrors.New("no layers to walk")
	}
	if delta.Same {
		return nil
	}
	if len(delta.Lower) == 0 && len(delta.Upper) == 1 {
		return WalkUpperdirChanges(ctx, changeFn, delta.Upper[0], upperView, lowerView, comparison)
	}
	paths, opaque, err := layerDeltaCandidates(ctx, slices.Concat(delta.Lower, delta.Upper))
	if err != nil {
		return err
	}
	// Descendants of a deleted, replaced or fully diffed directory sort right
	// after it.
	var skip string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if skip != "" && strings.HasPrefix(path, skip) {
			continue
		}
		skip = ""
		_, isOpaque := opaque[path]
		skipDescendants, err := diffLayerDeltaPath(ctx, changeFn, path, upperView, lowerView, isOpaque, comparison)
		if err != nil {
			return err
		}
		if skipDescendants {
			skip = path + string(os.PathSeparator)
		}
	}
	return nil
}

// layerDeltaCandidates lists every path any of layers holds an entry for,
// sorted as a double walk visits them, and the paths whose directory hides
// the layers beneath it: opaque ones, and those a layer replaced by a
// whiteout or any other non-directory.
func layerDeltaCandidates(ctx context.Context, layers []string) ([]string, map[string]struct{}, error) {
	candidates := map[string]struct{}{}
	opaque := map[string]struct{}{}
	for _, layer := range layers {
		err := filepath.Walk(layer, func(path string, f os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return context.Cause(ctx)
			}
			path, err = filepath.Rel(layer, path)
			if err != nil {
				return err
			}
			path = filepath.Join(string(os.PathSeparator), path)
			if path == string(os.PathSeparator) {
				return nil
			}
			if redirect, err := checkRedirect(layer, path, f); err != nil {
				return err
			} else if redirect {
				return pkgerrors.New("redirect_dir is used but it's not supported in overlayfs differ")
			}
			candidates[path] = struct{}{}
			if !f.IsDir() {
				// A whiteout, or any other non-directory, hides what the
				// layers beneath hold at the path: should a later layer
				// make it a directory again, that directory holds only
				// what the layers above wrote, as an opaque one would.
				opaque[path] = struct{}{}
				return nil
			}
			isOpaque, err := isOpaqueDir(filepath.Join(layer, path))
			if err != nil {
				return err
			}
			if isOpaque {
				opaque[path] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	slices.SortFunc(paths, directoryCompare)
	return paths, opaque, nil
}

// isOpaqueDir reports whether the overlay layer directory at path is marked
// opaque.
func isOpaqueDir(path string) (bool, error) {
	for _, key := range []string{"trusted.overlay.opaque", "user.overlay.opaque"} {
		value := make([]byte, 1)
		n, err := unix.Lgetxattr(path, key, value)
		if err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
			return false, pkgerrors.Wrapf(err, "failed to retrieve %s attr", key)
		}
		if err == nil && n == 1 && value[0] == 'y' {
			return true, nil
		}
	}
	return false, nil
}

// lstatIfExists is os.Lstat, with a path that does not exist reported as a
// nil FileInfo rather than an error.
func lstatIfExists(path string) (os.FileInfo, error) {
	f, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
		return nil, nil
	}
	return f, err
}

// diffLayerDeltaPath reports the changes at one candidate path of
// WalkLayerDeltaChanges by comparing the two merged views, and whether the
// path's descendants are covered already (deleted, replaced or listed in
// full) and must be skipped.
func diffLayerDeltaPath(
	ctx context.Context,
	changeFn continuityfs.ChangeFunc,
	path, upperView, lowerView string,
	opaque bool,
	comparison Comparison,
) (bool, error) {
	upperPath, lowerPath := filepath.Join(upperView, path), filepath.Join(lowerView, path)
	upperF, err := lstatIfExists(upperPath)
	if err != nil {
		return false, pkgerrors.Wrap(err, "failed to stat upper file during overlay diff")
	}
	lowerF, err := lstatIfExists(lowerPath)
	if err != nil {
		return false, pkgerrors.Wrap(err, "failed to stat lower file during overlay diff")
	}
	underPath := func(k continuityfs.ChangeKind, p string, f os.FileInfo, err error) error {
		return changeFn(k, filepath.Join(path, p), f, err)
	}
	// A directory upper has where lower has none holds only additions, but
	// not necessarily from the layers: with lower's own layers hiding a
	// shared directory, upper's copy is the shared one. List it, as a double
	// walk would.
	addDir := func() (bool, error) {
		if err := addDirChanges(underPath, upperPath); err != nil {
			return false, err
		}
		return true, nil
	}
	switch {
	case upperF == nil && lowerF == nil:
		// Added by one layer, removed by another.
		return false, nil
	case upperF == nil:
		if err := changeFn(continuityfs.ChangeKindDelete, path, nil, nil); err != nil {
			return false, err
		}
		return lowerF.IsDir(), nil
	case lowerF == nil:
		if err := changeFn(continuityfs.ChangeKindAdd, path, upperF, nil); err != nil {
			return false, err
		}
		if upperF.IsDir() {
			return addDir()
		}
		return false, nil
	}
	same, err := samePathInfo(lowerF, upperF, lowerPath, upperPath, comparison)
	if err != nil {
		return false, err
	}
	if !same {
		if err := changeFn(continuityfs.ChangeKindModify, path, upperF, nil); err != nil {
			return false, err
		}
	}
	switch {
	case lowerF.IsDir() && !upperF.IsDir():
		return true, nil
	case !lowerF.IsDir() && upperF.IsDir():
		return addDir()
	case lowerF.IsDir() && upperF.IsDir() && opaque:
		if err := WalkChanges(ctx, lowerPath, upperPath, comparison, underPath); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
