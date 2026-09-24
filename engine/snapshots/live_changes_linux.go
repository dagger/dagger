package snapshots

import (
	"context"
	"os"
	"strings"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/engine/snapshots/fsdiff"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// applyLiveChanges commits key as baseKey plus the changes held by the active
// snapshot activeKey, which must have been prepared on top of baseKey and may
// still be mounted and written to by a running container.
//
// The active snapshot is never mounted again: a second overlay mount of a
// live upperdir/workdir is undefined behavior. Instead, changes are read
// straight from its overlay upperdir (so the cost is proportional to what
// changed, not to the size of the tree), using mergedView -- the active
// snapshot's merged view as mounted by its user, e.g. /proc/<pid>/root/<dest>
// -- for content comparisons and opaque-directory walks.
//
// The new snapshot is prepared on top of baseKey, so base content is shared as
// overlay lowerdirs rather than copied. Changed files are always copied, never
// hard-linked: files in a live upperdir can still be modified in place, which
// would leak into the supposedly immutable result.
func (sn *mergeSnapshotter) applyLiveChanges(ctx context.Context, key, baseKey, activeKey, mergedView string) (rerr error) {
	if !sn.skipBaseLayers {
		// not overlay-based (or overlay without usable xattrs): no upperdir
		return ErrLiveChangesUnsupported
	}

	activeMntable, err := sn.Mounts(ctx, activeKey)
	if err != nil {
		return errors.Wrapf(err, "failed to get mounts of active snapshot %s", activeKey)
	}
	// Mount() only yields the mount specs here; nothing is mounted.
	activeMnts, releaseActive, err := activeMntable.Mount()
	if err != nil {
		return errors.Wrapf(err, "failed to get mount specs of active snapshot %s", activeKey)
	}
	defer releaseActive()
	if len(activeMnts) != 1 {
		return ErrLiveChangesUnsupported
	}
	var upperdir string
	switch mnt := activeMnts[0]; {
	case fsdiff.IsOverlayMountType(mnt):
		for _, opt := range mnt.Options {
			if strings.HasPrefix(opt, "upperdir=") {
				upperdir = strings.TrimPrefix(opt, "upperdir=")
			}
		}
	case (mnt.Type == "bind" || mnt.Type == "rbind") && baseKey == "":
		// With no lower layers the overlay snapshotter bind-mounts the
		// snapshot's upper directory directly: everything in it is an
		// addition, and there can be no whiteouts.
		upperdir = mnt.Source
	}
	if upperdir == "" {
		return ErrLiveChangesUnsupported
	}

	prepareKey := identity.NewID()
	if err := sn.Prepare(ctx, prepareKey, baseKey); err != nil {
		return errors.Wrapf(err, "failed to prepare %q", key)
	}
	usage, err := sn.applyLiveUpperdir(ctx, prepareKey, baseKey, upperdir, mergedView)
	if err != nil {
		return err
	}
	if err := sn.Commit(ctx, key, prepareKey, withMergeUsage(usage)); err != nil {
		return errors.Wrapf(err, "failed to commit %q", key)
	}
	return nil
}

func (sn *mergeSnapshotter) applyLiveUpperdir(ctx context.Context, prepareKey, baseKey, upperdir, mergedView string) (_ snapshots.Usage, rerr error) {
	applyMntable, err := sn.Mounts(ctx, prepareKey)
	if err != nil {
		return snapshots.Usage{}, errors.Wrapf(err, "failed to get mounts of %q", prepareKey)
	}
	// tryCrossSnapshotLink=false: never hardlink out of a live upperdir.
	a, err := applierFor(applyMntable, false, sn.userxattr)
	if err != nil {
		return snapshots.Usage{}, errors.Wrap(err, "failed to create applier")
	}
	defer func() {
		if err := a.Release(); err != nil {
			rerr = multierror.Append(rerr, errors.Wrap(err, "failed to release applier")).ErrorOrNil()
		}
	}()

	d := &differ{
		visited:    make(map[string]struct{}),
		inodes:     make(map[inode]string),
		comparison: fsdiff.CompareContentOnMetadataMatch,
		upperdir:   upperdir,
		upperRoot:  mergedView,
	}
	defer func() {
		rerr = multierror.Append(rerr, d.Release()).ErrorOrNil()
	}()

	if baseKey != "" {
		lowerMntable, err := sn.View(ctx, identity.NewID(), baseKey)
		if err != nil {
			return snapshots.Usage{}, errors.Wrapf(err, "failed to mount base snapshot view %s", baseKey)
		}
		mnts, release, err := lowerMntable.Mount()
		if err != nil {
			return snapshots.Usage{}, err
		}
		mounter := LocalMounterWithMounts(mnts)
		root, err := mounter.Mount()
		if err != nil {
			_ = release()
			return snapshots.Usage{}, err
		}
		d.lowerRoot = root
		d.releaseLower = func() error {
			err := mounter.Unmount()
			return multierror.Append(err, release()).ErrorOrNil()
		}
	} else {
		// scratch base: diff against an empty directory
		empty, err := os.MkdirTemp("", "live-changes-empty-base")
		if err != nil {
			return snapshots.Usage{}, err
		}
		d.lowerRoot = empty
		d.releaseLower = func() error { return os.RemoveAll(empty) }
	}

	if err := d.HandleChanges(ctx, a.Apply); err != nil {
		return snapshots.Usage{}, errors.Wrap(err, "failed to apply live changes")
	}
	if err := a.Flush(); err != nil {
		return snapshots.Usage{}, errors.Wrap(err, "failed to flush live changes")
	}
	return a.Usage()
}
