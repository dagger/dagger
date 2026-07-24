package core

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/layercopy"

	"github.com/dagger/dagger/dagql"
)

// SnapshotDirectory materializes a point-in-time, immutable Directory view of
// the cache volume's current (mutable) content via copy-on-read: it opens the
// live snapshot, copies its content into a fresh committed snapshot, and wraps
// that as a Directory. Reads against a cache mount resolve against this view,
// so they reflect the volume at read time (like a live host baseline). The
// copy is O(content); the result's snapshot digest is content-derived, so
// downstream reads of an unchanged cache still dedup.
func (cache *CacheVolume) SnapshotDirectory(ctx context.Context) (_ *Directory, rerr error) {
	if err := cache.InitializeSnapshot(ctx); err != nil {
		return nil, err
	}
	srcRef := cache.getSnapshot()
	if srcRef == nil {
		return nil, fmt.Errorf("cache volume %q has no snapshot", cache.Key)
	}
	selector := cache.getSnapshotSelector()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	newRef, err := query.SnapshotManager().New(
		ctx,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("cache volume %q snapshot", cache.Key)),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && newRef != nil {
			newRef.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, newRef, func(destRoot string, destMnt *mount.Mount) error {
		copier, err := layercopy.NewCopier(layercopy.Mount{Root: destRoot, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()
		return MountRef(ctx, srcRef, func(srcRoot string, srcMnt *mount.Mount) error {
			return copier.Copy(ctx,
				layercopy.Mount{Root: srcRoot, Mount: srcMnt},
				selector,
				"/",
				layercopy.CopyOptions{
					CopyDirContents: true,
					ReplaceExisting: true,
				},
			)
		}, mountRefAsReadOnly)
	})
	if err != nil {
		return nil, fmt.Errorf("copy cache volume content: %w", err)
	}

	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	newRef = nil

	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snap)
	return dir, nil
}

// CommitChanges applies a per-mount changeset delta into the cache volume's
// live mutable snapshot (write-through): added/modified content is copied in,
// removed paths are deleted. Callers hold the changeset from a mount edit;
// committing it makes containers/modules that mount the same cache volume
// observe the edits.
func (cache *CacheVolume) CommitChanges(ctx context.Context, changes *Changeset) error {
	if changes == nil {
		return nil
	}
	empty, err := changes.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if empty {
		return nil
	}
	if err := cache.InitializeSnapshot(ctx); err != nil {
		return err
	}
	ref := cache.getSnapshot()
	if ref == nil {
		return fmt.Errorf("cache volume %q has no snapshot", cache.Key)
	}
	return changes.CommitInto(ctx, ref, cache.getSnapshotSelector())
}

// CommitInto applies the changeset's delta into a mounted (mutable) ref at
// targetDir: removed paths are deleted, then added/modified content is copied
// in. It mirrors Directory.withChanges' snapshot application, but writes into
// an existing mutable ref in place rather than a fresh snapshot.
func (ch *Changeset) CommitInto(ctx context.Context, ref bkcache.MutableRef, targetDir string) (rerr error) {
	if targetDir == "" {
		targetDir = "/"
	}
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute paths: %w", err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}
	afterID, err := ch.After.ID()
	if err != nil {
		return fmt.Errorf("after ID: %w", err)
	}
	var dir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, ch.Before, &dir, dagql.Selector{
		Field: "diff",
		Args: []dagql.NamedInput{
			{Name: "other", Value: dagql.NewID[*Directory](afterID)},
		},
	}); err != nil {
		return fmt.Errorf("get changeset diff directory: %w", err)
	}
	engineCache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := engineCache.Evaluate(ctx, dir); err != nil {
		return fmt.Errorf("evaluate changeset diff directory: %w", err)
	}
	diffSnapshot, err := dir.Self().Snapshot.GetOrEval(ctx, dir.Result)
	if err != nil {
		return fmt.Errorf("evaluate changeset diff snapshot: %w", err)
	}
	diffSelector, err := dir.Self().Dir.GetOrEval(ctx, dir.Result)
	if err != nil {
		return fmt.Errorf("evaluate changeset diff selector: %w", err)
	}

	return MountRef(ctx, ref, func(root string, destMnt *mount.Mount) error {
		copier, err := layercopy.NewCopier(layercopy.Mount{Root: root, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()

		if err := removeChangesetPaths(root, targetDir, paths.Removed); err != nil {
			return err
		}

		if diffSnapshot != nil {
			err = MountRef(ctx, diffSnapshot, func(srcRoot string, srcMnt *mount.Mount) error {
				return copier.Copy(ctx,
					layercopy.Mount{Root: srcRoot, Mount: srcMnt},
					diffSelector,
					targetDir,
					layercopy.CopyOptions{
						CopyDirContents: true,
						ReplaceExisting: true,
					},
				)
			}, mountRefAsReadOnly)
			if err != nil {
				return fmt.Errorf("copy changed paths into cache: %w", err)
			}
		}

		return mkdirChangesetAddedDirs(ctx, copier, targetDir, paths)
	})
}
