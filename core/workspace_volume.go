package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/layercopy"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// CacheVolumeSnapshotLazy defers materializing a cache volume's mutable
// content into an immutable Directory until something actually needs it.
// Mounting a cache into a workspace is then free — the O(content) copy is paid
// by the first read, search, glob or export that touches the mount, and only
// once, since the evaluated snapshot is memoized on the Directory.
type CacheVolumeSnapshotLazy struct {
	LazyState
	Volume dagql.ObjectResult[*CacheVolume]
}

func (lazy *CacheVolumeSnapshotLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "CacheVolume.__snapshotDirectory", func(ctx context.Context) error {
		if lazy.Volume.Self() == nil {
			return fmt.Errorf("cache volume snapshot: missing volume")
		}
		return lazy.Volume.Self().snapshotInto(ctx, dir)
	})
}

func (lazy *CacheVolumeSnapshotLazy) AttachDependencies(
	ctx context.Context,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if lazy.Volume.Self() == nil {
		return nil, nil
	}
	attached, err := attach(lazy.Volume)
	if err != nil {
		return nil, fmt.Errorf("attach cache volume snapshot volume: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*CacheVolume])
	if !ok {
		return nil, fmt.Errorf("attach cache volume snapshot volume: unexpected result %T", attached)
	}
	lazy.Volume = typed
	return []dagql.AnyResult{typed}, nil
}

func (lazy *CacheVolumeSnapshotLazy) EncodePersisted(
	ctx context.Context,
	cache dagql.PersistedObjectCache,
) (json.RawMessage, error) {
	_ = ctx
	volumeID, err := encodePersistedObjectRef(cache, lazy.Volume, "cache volume snapshot volume")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedCacheVolumeSnapshotLazy{VolumeResultID: volumeID})
}

// SnapshotDirectory returns a point-in-time, immutable Directory view of the
// cache volume's current (mutable) content. The copy that produces it is
// deferred (see CacheVolumeSnapshotLazy), so the volume is read when the
// Directory is first evaluated rather than when this is called. The resulting
// snapshot's digest is content-derived, so downstream reads of an unchanged
// cache still dedup.
func (cache *CacheVolume) SnapshotDirectory(
	ctx context.Context,
	self dagql.ObjectResult[*CacheVolume],
) (*Directory, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return &Directory{
		Platform: query.Platform(),
		Lazy: &CacheVolumeSnapshotLazy{
			LazyState: NewLazyState(),
			Volume:    self,
		},
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}, nil
}

// snapshotInto copies the volume's live content into a fresh committed
// snapshot and stores it on dir. It is the deferred half of SnapshotDirectory.
func (cache *CacheVolume) snapshotInto(ctx context.Context, dir *Directory) (rerr error) {
	if err := cache.InitializeSnapshot(ctx); err != nil {
		return err
	}
	srcRef := cache.getSnapshot()
	if srcRef == nil {
		return fmt.Errorf("cache volume %q has no snapshot", cache.Key)
	}
	selector := cache.getSnapshotSelector()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}

	newRef, err := query.SnapshotManager().New(
		ctx,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("cache volume %q snapshot", cache.Key)),
	)
	if err != nil {
		return err
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
		return fmt.Errorf("copy cache volume content: %w", err)
	}

	snap, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	newRef = nil

	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snap)
	return nil
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
	if err := changes.CommitInto(ctx, ref, cache.getSnapshotSelector()); err != nil {
		return err
	}
	// The volume's content just changed; invalidate snapshot reads taken at
	// earlier write generations so other readers observe the commit.
	// Best-effort: the commit itself succeeded.
	if err := cache.BumpWriteGeneration(); err != nil {
		slog.Warn("could not bump cache volume write generation after commit",
			"cacheVolume", cache.Key, "error", err)
	}
	return nil
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
