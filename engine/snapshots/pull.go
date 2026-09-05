package snapshots

import (
	"context"
	"io"

	"fmt"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/internal/buildkit/identity"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	telemetry "github.com/dagger/otel-go"
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
) (_ ImmutableRef, rerr error) {
	if img == nil {
		return nil, errors.New("import image: nil image")
	}
	if opts.RecordType == "" {
		opts.RecordType = client.UsageRecordTypeRegular
	}
	pin, ctx, err := cm.newResourcePin(ctx)
	if err != nil {
		return nil, err
	}
	var current ImmutableRef
	defer func() {
		if rerr != nil {
			if current != nil {
				_ = current.Release(context.WithoutCancel(ctx))
			}
			_ = pin.release(ctx)
		}
	}()

	// Encapsulated like the resolver's "pulling" span: hidden unless it
	// fails, with its streaming progress surfacing on visible ancestors
	// (e.g. the originating Container.from call).
	name := opts.ImageRef
	if name == "" {
		name = img.Ref
	}
	span, ctx := tracing.StartSpan(ctx, "unpacking "+DisplayRef(name), telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()

	for _, layer := range img.Layers {
		next, err := cm.importLayer(ctx, layer, current, nil, opts)
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

	if err := cm.AttachLease(ctx, pin.id, current.SnapshotID()); err != nil {
		return nil, err
	}
	current.(*immutableRef).pin = pin
	return current, nil
}

func (cm *snapshotManager) importLayer(
	ctx context.Context,
	desc ocispecs.Descriptor,
	parent ImmutableRef,
	provider content.Provider,
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	blobKey := ImportedLayerBlobKey{
		ParentSnapshotID: parentSnapshotID,
		BlobDigest:       desc.Digest,
	}
	diffKey := ImportedLayerDiffKey{
		ParentSnapshotID: parentSnapshotID,
		DiffID:           diffID,
	}

	cm.mu.Lock()
	byBlob := cm.importedLayerByBlob[blobKey]
	byDiff := cm.importedLayerByDiff[diffKey]
	cm.mu.Unlock()
	leaseID, _ := leases.FromContext(ctx)
	for _, snapshotID := range []string{byBlob, byDiff} {
		if snapshotID == "" {
			continue
		}
		err := cm.AttachLease(ctx, leaseID, snapshotID)
		if err == nil {
			ref, err := cm.GetBySnapshotID(ctx, snapshotID, NoUpdateLastUsed)
			if err != nil {
				return nil, err
			}
			imported := ref.(*immutableRef)
			if err := setImportedImageMetadata(imported, opts); err != nil {
				_ = ref.Release(context.WithoutCancel(ctx))
				return nil, err
			}
			return ref, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
		cm.mu.Lock()
		if cm.importedLayerByBlob[blobKey] == snapshotID {
			delete(cm.importedLayerByBlob, blobKey)
		}
		if cm.importedLayerByDiff[diffKey] == snapshotID {
			delete(cm.importedLayerByDiff, diffKey)
		}
		cm.mu.Unlock()
	}

	if err := cm.importLayerContent(ctx, desc, provider); err != nil {
		return nil, err
	}
	if parent != nil {
		if err := cm.AttachLease(ctx, leaseID, parent.SnapshotID()); err != nil {
			return nil, err
		}
	}

	mut, err := cm.New(
		ctx,
		parent,
		nil,
		WithRecordType(opts.RecordType),
		WithDescription(fmt.Sprintf("import snapshot layer %s", desc.Digest)),
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
	var applyOpts []diff.ApplyOpt
	var unpack *ProgressTracker
	if desc.Size > 0 {
		unpack = NewProgressTracker(ctx, desc.Digest.String(), desc.Size, "bytes")
		applyOpts = append(applyOpts, diff.WithProgress(func(_ ocispecs.Descriptor, read int64) {
			unpack.Update(read)
		}))
	}
	if _, err := cm.Applier.Apply(ctx, desc, mounts, applyOpts...); err != nil {
		_ = unmount()
		return nil, err
	}
	if unpack != nil {
		// a successful apply consumed the whole blob even if the
		// decompressor skipped trailing bytes
		unpack.Update(desc.Size)
		unpack.Finish()
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

	if err := cm.AttachLease(ctx, leaseID, ref.SnapshotID()); err != nil {
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
	ctx, err := EnsureLease(ctx)
	if err != nil {
		return errors.Wrap(err, "ensure lease for content")
	}
	leaseID, ok := leases.FromContext(ctx)
	if !ok || leaseID == "" {
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

func setImportedImageMetadata(ref *immutableRef, opts ImportImageOpts) error {
	if opts.RecordType != "" {
		if err := ref.SetRecordType(opts.RecordType); err != nil {
			return err
		}
	}
	if opts.ImageRef != "" {
		return setImageRefMetadata(ref.md, WithImageRef(opts.ImageRef))
	}
	return nil
}

// importLayerContent pins local bytes before asking the supplied provider.
// Writer acquisition checks again, before ReaderAt, if another key supplied
// the same blob since our first lookup.
func (cm *snapshotManager) importLayerContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider) (rerr error) {
	present, err := cm.pinContent(ctx, desc)
	if err != nil || present {
		return err
	}
	if provider == nil {
		return errors.Wrapf(cerrdefs.ErrNotFound, "missing local layer %s", desc.Digest)
	}
	ref := "snapshot-import-" + identity.NewID()
	writer, err := content.OpenWriter(ctx, cm.ContentStore, content.WithRef(ref), content.WithDescriptor(desc))
	if cerrdefs.IsAlreadyExists(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = writer.Close()
		if rerr != nil {
			_ = cm.ContentStore.Abort(context.WithoutCancel(ctx), ref)
		}
	}()
	reader, err := provider.ReaderAt(ctx, desc)
	if err != nil {
		return err
	}
	defer reader.Close()
	return content.Copy(ctx, writer, io.NewSectionReader(reader, 0, reader.Size()), desc.Size, desc.Digest)
}
