package snapshots

import (
	"context"
	"strconv"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/engine/snapshots/fsdiff"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/moby/sys/userns"
	"github.com/pkg/errors"
)

var hardlinkMergeSnapshotters = map[string]struct{}{
	"native":    {},
	"overlayfs": {},
}

var overlayBasedSnapshotters = map[string]struct{}{
	"overlayfs": {},
	"stargz":    {},
}

type Diff struct {
	Lower      string
	Upper      string
	Comparison fsdiff.Comparison
}

type MergeSnapshotter interface {
	Snapshotter
	Merge(ctx context.Context, key string, diffs []Diff, opts ...snapshots.Opt) error
}

type mergeSnapshotter struct {
	Snapshotter
	lm leases.Manager

	tryCrossSnapshotLink bool
	skipBaseLayers       bool
	userxattr            bool
}

func NewMergeSnapshotter(ctx context.Context, sn Snapshotter, lm leases.Manager) MergeSnapshotter {
	name := sn.Name()
	_, tryCrossSnapshotLink := hardlinkMergeSnapshotters[name]
	_, overlayBased := overlayBasedSnapshotters[name]

	skipBaseLayers := overlayBased
	var userxattr bool
	if overlayBased && userns.RunningInUserNS() {
		var err error
		userxattr, err = needsUserXAttr(ctx, sn, lm)
		if err != nil {
			bklog.G(ctx).Debugf("failed to check user xattr: %v", err)
			tryCrossSnapshotLink = false
			skipBaseLayers = false
		} else {
			tryCrossSnapshotLink = tryCrossSnapshotLink && userxattr
			skipBaseLayers = userxattr
		}
	}

	return &mergeSnapshotter{
		Snapshotter:          sn,
		lm:                   lm,
		tryCrossSnapshotLink: tryCrossSnapshotLink,
		skipBaseLayers:       skipBaseLayers,
		userxattr:            userxattr,
	}
}

func (sn *mergeSnapshotter) Merge(ctx context.Context, key string, diffs []Diff, opts ...snapshots.Opt) error {
	var baseKey string
	if sn.skipBaseLayers {
		var baseIndex int
		for i, diff := range diffs {
			var parentKey string
			if diff.Upper != "" {
				info, err := sn.Stat(ctx, diff.Upper)
				if err != nil {
					return err
				}
				parentKey = info.Parent
			}
			if parentKey != diff.Lower {
				break
			}
			if diff.Lower != baseKey {
				break
			}
			baseKey = diff.Upper
			baseIndex = i + 1
		}
		diffs = diffs[baseIndex:]
	}

	if leaseID, ok := leases.FromContext(ctx); !ok || leaseID == "" {
		leaseCtx, err := EnsureLease(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to ensure temporary lease for view mounts during merge")
		}
		ctx = leaseCtx
	}
	if leaseID, ok := leases.FromContext(ctx); !ok || leaseID == "" {
		leaseCtx, done, err := WithLease(ctx, sn.lm, MakeTemporary)
		if err != nil {
			return errors.Wrap(err, "failed to create temporary lease for view mounts during merge")
		}
		defer done(context.TODO())
		ctx = leaseCtx
	}

	prepareKey := identity.NewID()
	if err := sn.Prepare(ctx, prepareKey, baseKey); err != nil {
		return errors.Wrapf(err, "failed to prepare %q", key)
	}
	applyMounts, err := sn.Mounts(ctx, prepareKey)
	if err != nil {
		return errors.Wrapf(err, "failed to get mounts of %q", key)
	}

	usage, err := sn.diffApply(ctx, applyMounts, diffs...)
	if err != nil {
		return errors.Wrap(err, "failed to apply diffs")
	}
	if err := sn.Commit(ctx, key, prepareKey, withMergeUsage(usage)); err != nil {
		return errors.Wrapf(err, "failed to commit %q", key)
	}
	return nil
}

func (sn *mergeSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	if info, err := sn.Stat(ctx, key); err != nil {
		return snapshots.Usage{}, err
	} else if usage, ok, err := mergeUsageOf(info); err != nil {
		return snapshots.Usage{}, err
	} else if ok {
		return usage, nil
	}
	return sn.Snapshotter.Usage(ctx, key)
}

const mergeUsageSizeLabel = "buildkit.mergeUsageSize"
const mergeUsageInodesLabel = "buildkit.mergeUsageInodes"

func withMergeUsage(usage snapshots.Usage) snapshots.Opt {
	return snapshots.WithLabels(map[string]string{
		mergeUsageSizeLabel:   strconv.Itoa(int(usage.Size)),
		mergeUsageInodesLabel: strconv.Itoa(int(usage.Inodes)),
	})
}

func mergeUsageOf(info snapshots.Info) (usage snapshots.Usage, ok bool, rerr error) {
	if info.Labels == nil {
		return snapshots.Usage{}, false, nil
	}
	hasMergeUsageLabel := false
	if str, ok := info.Labels[mergeUsageSizeLabel]; ok {
		i, err := strconv.Atoi(str)
		if err != nil {
			return snapshots.Usage{}, false, err
		}
		usage.Size = int64(i)
		hasMergeUsageLabel = true
	}
	if str, ok := info.Labels[mergeUsageInodesLabel]; ok {
		i, err := strconv.Atoi(str)
		if err != nil {
			return snapshots.Usage{}, false, err
		}
		usage.Inodes = int64(i)
		hasMergeUsageLabel = true
	}
	if !hasMergeUsageLabel {
		return snapshots.Usage{}, false, nil
	}
	return usage, true, nil
}
