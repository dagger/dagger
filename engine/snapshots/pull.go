package snapshots

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func importedLayerDiffLockKey(parentSnapshotID string, diffID digest.Digest) string {
	return parentSnapshotID + "\x00" + diffID.String()
}

func (cm *snapshotManager) ImportImage(
	ctx context.Context,
	img *ImportedImage,
	opts ImportImageOpts,
) (current ImmutableRef, rerr error) {
	if img == nil {
		return nil, errors.New("import image: nil image")
	}
	if opts.RecordType == "" {
		opts.RecordType = client.UsageRecordTypeRegular
	}
	defer func() {
		if rerr != nil && current != nil {
			_ = current.Release(context.WithoutCancel(ctx))
		}
	}()

	for _, layer := range img.Layers {
		next, err := cm.importImageLayer(ctx, layer, current, opts)
		if err != nil {
			return nil, err
		}
		if current != nil {
			_ = current.Release(context.WithoutCancel(ctx))
		}
		current = next
	}

	if current == nil {
		mut, err := cm.New(
			ctx,
			nil,
			nil,
			WithRecordType(opts.RecordType),
			WithDescription("import image rootfs (empty)"),
			WithImageRef(opts.ImageRef),
		)
		if err != nil {
			return nil, err
		}
		defer func() {
			if mut != nil {
				_ = mut.Release(context.WithoutCancel(ctx))
			}
		}()

		ref, err := mut.Commit(ctx)
		if err != nil {
			return nil, err
		}
		mut = nil
		current = ref

		currentRef, ok := current.(*immutableRef)
		if !ok {
			return nil, fmt.Errorf("import image empty rootfs: unexpected ref type %T", current)
		}
		if err := currentRef.SetRecordType(opts.RecordType); err != nil {
			return nil, err
		}
		if opts.ImageRef != "" {
			if err := setImageRefMetadata(currentRef.md, WithImageRef(opts.ImageRef)); err != nil {
				return nil, err
			}
		}
	}

	topLevelContent := []ocispecs.Descriptor{img.ManifestDesc, img.ConfigDesc}
	topLevelContent = append(topLevelContent, img.Nonlayers...)

	seen := map[digest.Digest]struct{}{}
	for _, desc := range topLevelContent {
		if desc.Digest == "" {
			continue
		}
		if _, ok := seen[desc.Digest]; ok {
			continue
		}
		seen[desc.Digest] = struct{}{}

		if err := cm.linkContentToContextLease(ctx, desc); err != nil {
			return nil, err
		}
		if err := cm.recordSnapshotContent(current.SnapshotID(), desc); err != nil {
			return nil, err
		}
	}

	return current, nil
}

func (cm *snapshotManager) importImageLayer(
	ctx context.Context,
	desc ocispecs.Descriptor,
	parent ImmutableRef,
	opts ImportImageOpts,
) (ImmutableRef, error) {
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return nil, err
	}

	parentSnapshotID := ""
	if parent != nil {
		parentSnapshotID = parent.SnapshotID()
	}

	lockKey := importedLayerDiffLockKey(parentSnapshotID, diffID)
	cm.importLayerLocker.Lock(lockKey)
	defer cm.importLayerLocker.Unlock(lockKey)

	blobKey := ImportedLayerBlobKey{
		ParentSnapshotID: parentSnapshotID,
		BlobDigest:       desc.Digest,
	}
	diffKey := ImportedLayerDiffKey{
		ParentSnapshotID: parentSnapshotID,
		DiffID:           diffID,
	}

	if desc.Digest != "" {
		cm.mu.Lock()
		existingSnapshotID, ok := cm.importedLayerByBlob[blobKey]
		cm.mu.Unlock()
		if ok {
			ref, err := cm.GetBySnapshotID(ctx, existingSnapshotID, NoUpdateLastUsed)
			if err == nil {
				imported, ok := ref.(*immutableRef)
				if !ok {
					_ = ref.Release(context.WithoutCancel(ctx))
					return nil, fmt.Errorf("import image dedupe by blob: unexpected ref type %T", ref)
				}
				if opts.RecordType != "" {
					if err := imported.SetRecordType(opts.RecordType); err != nil {
						_ = ref.Release(context.WithoutCancel(ctx))
						return nil, err
					}
				}
				if opts.ImageRef != "" {
					if err := setImageRefMetadata(imported.md, WithImageRef(opts.ImageRef)); err != nil {
						_ = ref.Release(context.WithoutCancel(ctx))
						return nil, err
					}
				}
				return ref, nil
			}
			if !IsNotFound(err) {
				return nil, err
			}
			cm.mu.Lock()
			delete(cm.importedLayerByBlob, blobKey)
			cm.mu.Unlock()
		}
	}

	cm.mu.Lock()
	existingSnapshotID, ok := cm.importedLayerByDiff[diffKey]
	cm.mu.Unlock()
	if ok {
		ref, err := cm.GetBySnapshotID(ctx, existingSnapshotID, NoUpdateLastUsed)
		if err == nil {
			imported, ok := ref.(*immutableRef)
			if !ok {
				_ = ref.Release(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("import image dedupe by diff: unexpected ref type %T", ref)
			}
			if opts.RecordType != "" {
				if err := imported.SetRecordType(opts.RecordType); err != nil {
					_ = ref.Release(context.WithoutCancel(ctx))
					return nil, err
				}
			}
			if opts.ImageRef != "" {
				if err := setImageRefMetadata(imported.md, WithImageRef(opts.ImageRef)); err != nil {
					_ = ref.Release(context.WithoutCancel(ctx))
					return nil, err
				}
			}
			return ref, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
		cm.mu.Lock()
		delete(cm.importedLayerByDiff, diffKey)
		cm.mu.Unlock()
	}

	mut, err := cm.New(
		ctx,
		parent,
		nil,
		WithRecordType(opts.RecordType),
		WithDescription(fmt.Sprintf("import image layer %s", desc.Digest)),
		WithImageRef(opts.ImageRef),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if mut != nil {
			_ = mut.Release(context.WithoutCancel(ctx))
		}
	}()

	mountable, err := mut.Mount(ctx, false)
	if err != nil {
		return nil, err
	}
	mounts, unmount, err := mountable.Mount()
	if err != nil {
		return nil, err
	}
	if _, err := cm.Applier.Apply(ctx, desc, mounts); err != nil {
		_ = unmount()
		return nil, err
	}
	if err := unmount(); err != nil {
		return nil, err
	}

	ref, err := mut.Commit(ctx)
	if err != nil {
		return nil, err
	}
	mut = nil

	imported, ok := ref.(*immutableRef)
	if !ok {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("import image layer %s: unexpected ref type %T", desc.Digest, ref)
	}

	if err := imported.md.queueDiffID(diffID); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := imported.md.queueBlob(desc.Digest); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := imported.md.queueBlobOnly(false); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := imported.md.queueMediaType(desc.MediaType); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := imported.md.queueBlobSize(desc.Size); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := imported.md.appendURLs(desc.URLs); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if opts.RecordType != "" {
		if err := imported.SetRecordType(opts.RecordType); err != nil {
			_ = ref.Release(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if opts.ImageRef != "" {
		if err := setImageRefMetadata(imported.md, WithImageRef(opts.ImageRef)); err != nil {
			_ = ref.Release(context.WithoutCancel(ctx))
			return nil, err
		}
	}

	info, err := cm.ContentStore.Info(ctx, desc.Digest)
	if err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	info = addBlobDescToInfo(desc, info)
	if _, err := cm.ContentStore.Update(ctx, info, fieldsFromLabels(info.Labels)...); err != nil && !cerrdefs.IsNotFound(err) {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}

	if err := cm.linkContentToContextLease(ctx, desc); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
	}

	cm.mu.Lock()
	cm.importedLayerByBlob[blobKey] = ref.SnapshotID()
	cm.importedLayerByDiff[diffKey] = ref.SnapshotID()
	cm.mu.Unlock()

	return ref, nil
}

func (cm *snapshotManager) linkContentToContextLease(ctx context.Context, desc ocispecs.Descriptor) error {
	if desc.Digest == "" {
		return nil
	}
	leaseID, ok := leases.FromContext(ctx)
	if !ok {
		return nil
	}
	if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
		ID:   desc.Digest.String(),
		Type: "content",
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return errors.Wrapf(err, "attach content %s to lease %s", desc.Digest, leaseID)
	}
	return nil
}

func (cm *snapshotManager) recordSnapshotContent(snapshotID string, desc ocispecs.Descriptor) error {
	if snapshotID == "" || desc.Digest == "" {
		return nil
	}
	cm.mu.Lock()
	if cm.snapshotContentDigests[snapshotID] == nil {
		cm.snapshotContentDigests[snapshotID] = make(map[digest.Digest]struct{})
	}
	cm.snapshotContentDigests[snapshotID][desc.Digest] = struct{}{}
	leaseIDs := make([]string, 0, len(cm.snapshotOwnerLeases[snapshotID]))
	for leaseID := range cm.snapshotOwnerLeases[snapshotID] {
		leaseIDs = append(leaseIDs, leaseID)
	}
	cm.mu.Unlock()

	for _, leaseID := range leaseIDs {
		err := cm.LeaseManager.AddResource(context.WithoutCancel(context.TODO()), leases.Lease{ID: leaseID}, leases.Resource{
			ID:   desc.Digest.String(),
			Type: "content",
		})
		if err != nil && !cerrdefs.IsAlreadyExists(err) && !cerrdefs.IsNotFound(err) {
			return errors.Wrapf(err, "attach content %s to owner lease %s", desc.Digest, leaseID)
		}
	}
	return nil
}
