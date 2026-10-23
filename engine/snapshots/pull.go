package snapshots

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
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
		next, err := cm.importLayer(ctx, layer, current, nil, opts, false, nil, nil)
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
	chainMode bool,
	timing *ImportTiming,
	chain *chainLayer,
) (ImmutableRef, error) {
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return nil, err
	}

	parentSnapshotID := ""
	if parent != nil {
		parentSnapshotID = parent.SnapshotID()
	}

	// The stamps feed the import's timing sums and the profile alike.
	clock := importClock{timing: timing, prof: wcprof.Enabled(ctx)}
	unlock, err := cm.acquireImportLock(ctx, importedLayerDiffLockKey(parentSnapshotID, diffID), clock)
	if err != nil {
		return nil, err
	}
	defer unlock()

	blobKey := ImportedLayerBlobKey{
		ParentSnapshotID: parentSnapshotID,
		BlobDigest:       desc.Digest,
	}
	diffKey := ImportedLayerDiffKey{
		ParentSnapshotID: parentSnapshotID,
		DiffID:           diffID,
	}
	if ref, err := cm.reuseImportedLayer(ctx, blobKey, diffKey, opts); err != nil || ref != nil {
		return ref, err
	}

	if err := cm.fetchLayerContent(ctx, desc, provider, chainMode, chain, clock); err != nil {
		return nil, err
	}
	if chain != nil && chain.fetched != nil {
		chain.fetched()
	}
	leaseID, _ := leases.FromContext(ctx)
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
	if err := cm.applyLayer(ctx, mut, desc, chainMode, clock, false); err != nil {
		return nil, err
	}

	ref, err := mut.Commit(ctx)
	if err != nil {
		return nil, err
	}
	mut = nil
	return cm.recordImportedLayer(ctx, ref, desc, diffID, opts, blobKey, diffKey)
}

// acquireImportLock takes the import lock key names, adding the wait to the
// clock's timing and profile.
func (cm *snapshotManager) acquireImportLock(ctx context.Context, key string, clock importClock) (func(), error) {
	waitStart := clock.now()
	unlock, err := cm.importLayerLocker.acquire(ctx, key)
	waited := clock.now()
	if clock.timing != nil {
		clock.timing.LayerWait += waited.Sub(waitStart)
	}
	if clock.prof {
		wcprof.RecordWaitAt(ctx, "snapshots.importLayer", wcprof.WaitReasonLock, waitStart, waited)
	}
	return unlock, err
}

// reuseImportedLayer returns the snapshot the engine already imported for a
// layer on the same parent, by its blob or its diff, or nil when it has none.
// Requires the layer's import lock.
func (cm *snapshotManager) reuseImportedLayer(ctx context.Context, blobKey ImportedLayerBlobKey, diffKey ImportedLayerDiffKey, opts ImportImageOpts) (ImmutableRef, error) {
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
	return nil, nil
}

// fetchLayerContent gets desc's blob into the content store, linked to ctx's
// lease, adding the time to the clock's timing and profile.
func (cm *snapshotManager) fetchLayerContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, chainMode bool, chain *chainLayer, clock importClock) error {
	fetchStart := clock.now()
	source, joined, err := cm.importLayerContent(ctx, desc, provider, chainMode, chain)
	fetched := clock.now()
	if clock.timing != nil {
		clock.timing.Fetch += fetched.Sub(fetchStart)
		if joined {
			clock.timing.Joined++
		}
	}
	if clock.prof {
		recordImportPhase(ctx, "snapshot.import.fetch["+source+"]", desc, source, fetchStart, fetched, err)
	}
	return err
}

// applyLayer applies desc's blob to mut. apart says mut was prepared with no
// parent for a rebase on commit (cm.applyLayersApart): its directory is then
// written in place, with the layer's whiteouts as overlay whiteouts, as it is
// for a snapshot with parents.
func (cm *snapshotManager) applyLayer(ctx context.Context, mut MutableRef, desc ocispecs.Descriptor, chainMode bool, clock importClock, apart bool) error {
	mountable, err := mut.Mount(ctx, false)
	if err != nil {
		return err
	}
	mounts, unmount, err := mountable.Mount()
	if err != nil {
		return err
	}
	if apart {
		mounts = upperOverlay(mounts)
	}
	var applyOpts []diff.ApplyOpt
	var unpack *ProgressTracker
	if desc.Size > 0 {
		unpack = NewProgressTracker(ctx, desc.Digest.String(), desc.Size, "bytes")
		applyOpts = append(applyOpts, diff.WithProgress(func(_ ocispecs.Descriptor, read int64) {
			unpack.Update(read)
		}))
	}
	applyStart := clock.now()
	appliedDesc, err := cm.Applier.Apply(ctx, desc, mounts, applyOpts...)
	applied := clock.now()
	if clock.timing != nil {
		clock.timing.Apply += applied.Sub(applyStart)
	}
	if clock.prof {
		recordImportPhase(ctx, "snapshot.import.apply", desc, "", applyStart, applied, err)
	}
	if err != nil {
		_ = unmount()
		return chainError(ctx, chainMode, desc, "apply", err)
	}
	// The snapshot is found again by diffID, so its content must be the
	// diffID's, as containerd's unpacker also requires.
	diffID, err := diffIDFromDescriptor(desc)
	if err == nil && appliedDesc.Digest != diffID {
		err = errors.Errorf("wrong diff id %s calculated on extraction, expected %s", appliedDesc.Digest, diffID)
	}
	if err != nil {
		_ = unmount()
		return chainError(ctx, chainMode, desc, "apply", err)
	}
	if unpack != nil {
		// a successful apply consumed the whole blob even if the
		// decompressor skipped trailing bytes
		unpack.Update(desc.Size)
		unpack.Finish()
	}
	return unmount()
}

// upperOverlay turns the bind mount of a snapshot prepared with no parent
// into an overlay mount of its directory alone, as containerd's own parallel
// unpack does: containerd's applier then writes an overlay mount's upper
// directory in place, converting whiteouts, and never mounts it.
func upperOverlay(mounts []mount.Mount) []mount.Mount {
	if len(mounts) != 1 || mounts[0].Type != "bind" {
		return mounts
	}
	m := mount.Mount{Type: "overlay", Source: "overlay"}
	for _, o := range mounts[0].Options {
		if o != "rbind" {
			m.Options = append(m.Options, o)
		}
	}
	m.Options = append(m.Options, "upperdir="+mounts[0].Source)
	return []mount.Mount{m}
}

// recordImportedLayer records ref, just committed from desc's blob, as the
// layer's import on its parent: its blob metadata, its content and the
// engine's import indexes. It takes ownership of ref, which it releases on
// failure. Requires the layer's import lock.
func (cm *snapshotManager) recordImportedLayer(ctx context.Context, ref ImmutableRef, desc ocispecs.Descriptor, diffID digest.Digest, opts ImportImageOpts, blobKey ImportedLayerBlobKey, diffKey ImportedLayerDiffKey) (ImmutableRef, error) {
	imported, ok := ref.(*immutableRef)
	if !ok {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("import image layer %s: unexpected ref type %T", desc.Digest, ref)
	}

	// Before the blob metadata: a failed label leaves no reusable blob
	// record behind.
	if err := cm.labelSnapshotBlob(ctx, ref.SnapshotID(), desc.Digest); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, err
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

	leaseID, _ := leases.FromContext(ctx)
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

// BuiltinLayersLeaseID is the persistent lease holding the blobs a chain
// import copied from the builtin image store: builtin blobs, so engine
// lifetime like the rest of the builtin images' content.
const BuiltinLayersLeaseID = "dagger/builtin-image/layers"

// chainLayer is what a chain import passes each of its layer imports: the
// chain whose import it is, its download of the layer's blob started ahead,
// if any, and what to run once the layer's blob is in the content store.
type chainLayer struct {
	owner   *ExportChain
	ahead   *contentFetch
	fetched func()
}

// importLayerContent pins local bytes before asking the supplied provider.
// A chain import consults the builtin image store between the two: a layer
// of a builtin image is then copied from the engine's own files, with no
// network, and held for the engine's lifetime under BuiltinLayersLeaseID.
// The provider's download goes through the fetch coordinator, which joins a
// download of the same blob already in progress. It returns where the blob
// came from: present (already there, including a blob downloaded ahead that
// is in by then), builtin (the builtin image store), download (this import's
// own download), or joined (another import's download), and whether it
// joined another import's download.
func (cm *snapshotManager) importLayerContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, chainMode bool, chain *chainLayer) (string, bool, error) {
	present, err := cm.pinContent(ctx, desc)
	if err != nil || present {
		return layerSourcePresent, false, err
	}
	if chainMode {
		copied, err := cm.copyBuiltinLayer(ctx, desc)
		if err != nil {
			// The provider is the fallback, as a service outage never
			// blocks an import: log and download as today.
			bklog.G(ctx).WithError(err).Warnf("builtin image store could not supply layer %s; downloading it", desc.Digest)
		} else if copied {
			return layerSourceBuiltin, false, nil
		}
	}
	if provider == nil {
		return layerSourceDownload, false, chainError(ctx, chainMode, desc, "provider", errors.Wrapf(cerrdefs.ErrNotFound, "missing local layer %s", desc.Digest))
	}
	var owner *ExportChain
	var ahead *contentFetch
	if chain != nil {
		owner = chain.owner
		if chain.ahead != nil && chain.ahead.desc.Digest == desc.Digest {
			ahead = chain.ahead
		}
	}
	joined, read, err := cm.fetchChainContent(ctx, desc, provider, owner, ahead)
	return downloadSource(joined, read, err), joined, err
}

// downloadSource is where a layer's blob came from when the provider was
// asked for it: its import's own download, another import's it joined, or
// the content store, when another writer stored the blob first and nothing
// was read.
func downloadSource(joined, read bool, err error) string {
	switch {
	case err != nil:
		return layerSourceDownload
	case !read:
		return layerSourcePresent
	case joined:
		return layerSourceJoined
	default:
		return layerSourceDownload
	}
}

// Where importLayerContent found a layer's blob.
const (
	layerSourcePresent  = "present"
	layerSourceBuiltin  = "builtin"
	layerSourceDownload = "download"
	layerSourceJoined   = "joined"
)

// importClock stamps an import's phases when the import's timing or the
// profile wants them, and reads no clock otherwise.
type importClock struct {
	timing *ImportTiming
	prof   bool
}

func (c importClock) now() time.Time {
	if c.timing == nil && !c.prof {
		return time.Time{}
	}
	return time.Now()
}

// recordImportPhase records one phase of a layer's import, from start to
// end, as a profile op: the layer's digest, and its size when the phase
// moved its bytes (an import's own download or an apply, not a blob already
// present or another import's download it joined). A fetch phase is the
// time the import still waited for the blob: a download started ahead may
// have ended before it, and the blob is then present. A download, own or
// joined, waits on the network; the rest is the engine's.
func recordImportPhase(ctx context.Context, class string, desc ocispecs.Descriptor, source string, start, end time.Time, err error) {
	opts := wcprof.OpOpts{Ident: desc.Digest.String(), Bytes: desc.Size}
	switch source {
	case layerSourceDownload:
		opts.WorkType = wcprof.WorkTypeExternal
	case layerSourceJoined:
		opts.WorkType = wcprof.WorkTypeExternal
		opts.Bytes = 0
	case layerSourcePresent:
		opts.Bytes = 0
	}
	outcome := wcprof.OutcomeOK
	if err != nil {
		outcome = wcprof.OutcomeError
	}
	wcprof.RecordOpAt(ctx, wcprof.OpKindIO, class, opts, start, end, outcome)
}

// copyBuiltinLayer copies the blob desc names from the builtin image store
// into the content store when that store has exactly that digest with the
// same size, binds it to the context lease and to the builtin layers lease,
// and reports whether it did. A store without the digest reports false with
// no error; a failure to read or write is an error for the caller to fall
// through on. Nothing but the exact digest matches: a re-diffed form of a
// builtin layer has another digest and is not found here.
func (cm *snapshotManager) copyBuiltinLayer(ctx context.Context, desc ocispecs.Descriptor) (copied bool, rerr error) {
	if cm.builtinContent == nil || desc.Digest == "" {
		return false, nil
	}
	info, err := cm.builtinContent.Info(ctx, desc.Digest)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrapf(err, "look up %s in the builtin image store", desc.Digest)
	}
	// Exact digest and exact size, always: a layer that says another size,
	// zero included, is not this blob.
	if info.Size != desc.Size {
		return false, errors.Errorf("builtin image store has %s with size %d, the layer says %d", desc.Digest, info.Size, desc.Size)
	}
	ref := "builtin-layer-" + rand.Text()
	writer, err := content.OpenWriter(ctx, cm.ContentStore, content.WithRef(ref), content.WithDescriptor(desc))
	if cerrdefs.IsAlreadyExists(err) {
		return cm.pinBuiltinLayer(ctx, desc)
	}
	if err != nil {
		return false, err
	}
	defer func() {
		_ = writer.Close()
		if rerr != nil {
			_ = cm.ContentStore.Abort(context.WithoutCancel(ctx), ref)
		}
	}()
	reader, err := cm.builtinContent.ReaderAt(ctx, ocispecs.Descriptor{Digest: desc.Digest, Size: info.Size})
	if err != nil {
		return false, errors.Wrapf(err, "read %s from the builtin image store", desc.Digest)
	}
	defer reader.Close()
	if err := content.Copy(ctx, writer, io.NewSectionReader(reader, 0, reader.Size()), info.Size, desc.Digest); err != nil {
		return false, errors.Wrapf(err, "copy %s from the builtin image store", desc.Digest)
	}
	return cm.pinBuiltinLayer(ctx, desc)
}

// pinBuiltinLayer binds a blob the builtin store supplied to the context
// lease, so the import's own protection covers it, and to the persistent
// builtin layers lease.
func (cm *snapshotManager) pinBuiltinLayer(ctx context.Context, desc ocispecs.Descriptor) (bool, error) {
	present, err := cm.pinContent(ctx, desc)
	if err != nil {
		return false, err
	}
	if !present {
		return false, errors.Errorf("builtin layer %s is missing after its copy", desc.Digest)
	}
	if err := cm.PinContent(ctx, BuiltinLayersLeaseID, []ocispecs.Descriptor{desc}); err != nil {
		return false, err
	}
	bklog.G(ctx).Debugf("layer %s copied from the builtin image store", desc.Digest)
	return true, nil
}
