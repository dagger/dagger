package snapshots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/labels"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots/config"
	overlay "github.com/dagger/dagger/engine/snapshots/fsdiff"
	rootlessmountopts "github.com/dagger/dagger/engine/snapshots/rootlessmountopts"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/moby/sys/mountinfo"
	"github.com/moby/sys/userns"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var additionalAnnotations = append(append(compression.EStargzAnnotations, labels.LabelUncompressed), "buildkit/createdat")

// Ref is a reference to cacheable objects.
type Ref interface {
	Mountable
	ID() string
	SnapshotID() string
	Release(context.Context) error
	Size(context.Context) (int64, error)
}

type ImmutableRef interface {
	Ref
	ExportChain(ctx context.Context, cfg config.RefConfig) (*ExportChain, error)
}

type MutableRef interface {
	Ref
	Commit(context.Context) (ImmutableRef, error)
	InvalidateSize(context.Context) error
}

type Mountable interface {
	Mount(ctx context.Context, readonly bool) (snapshot.Mountable, error)
}

type refMetadata struct {
	snapshotID string
	md         *cacheMetadata
}

func (rm refMetadata) ID() string {
	return rm.snapshotID
}

func (rm refMetadata) SnapshotID() string {
	return rm.snapshotID
}

func (rm refMetadata) GetDescription() string {
	return rm.md.GetDescription()
}

func (rm refMetadata) SetDescription(descr string) error {
	return rm.md.SetDescription(descr)
}

func (rm refMetadata) GetCreatedAt() time.Time {
	return rm.md.GetCreatedAt()
}

func (rm refMetadata) SetCreatedAt(tm time.Time) error {
	return rm.md.SetCreatedAt(tm)
}

func (rm refMetadata) GetLayerType() string {
	return rm.md.GetLayerType()
}

func (rm refMetadata) SetLayerType(layerType string) error {
	return rm.md.SetLayerType(layerType)
}

func (rm refMetadata) GetRecordType() client.UsageRecordType {
	return rm.md.GetRecordType()
}

func (rm refMetadata) SetRecordType(recordType client.UsageRecordType) error {
	return rm.md.SetRecordType(recordType)
}

func (rm refMetadata) GetString(key string) string {
	return rm.md.GetString(key)
}

func (rm refMetadata) Get(key string) *Value {
	return rm.md.Get(key)
}

func (rm refMetadata) SetString(key, value string, index string) error {
	return rm.md.SetString(key, value, index)
}

func (rm refMetadata) GetExternal(key string) ([]byte, error) {
	return rm.md.GetExternal(key)
}

func (rm refMetadata) SetExternal(key string, dt []byte) error {
	return rm.md.SetExternal(key, dt)
}

func (rm refMetadata) ClearValueAndIndex(key string, index string) error {
	return rm.md.ClearValueAndIndex(key, index)
}

type cacheRecord struct {
	cm      *snapshotManager
	mutable bool
	locked  bool
	md      *cacheMetadata

	// dead means record is marked as deleted
	dead bool
}

func (cr *cacheRecord) isDead() bool {
	return cr.dead
}

func (cr *cacheRecord) ID() string {
	return cr.md.ID()
}

func sizeFromMetadata(
	ctx context.Context,
	sizeG *flightcontrol.Group[int64],
	cm *snapshotManager,
	md *cacheMetadata,
	snapshotID string,
) (int64, error) {
	return sizeG.Do(ctx, snapshotID, func(ctx context.Context) (int64, error) {
		s := md.getSize()
		if s != sizeUnknown {
			return s, nil
		}

		var usage snapshots.Usage
		if !md.getBlobOnly() {
			var err error
			usage, err = cm.Snapshotter.Usage(ctx, snapshotID)
			if err != nil && !errors.Is(err, cerrdefs.ErrNotFound) {
				return s, errors.Wrapf(err, "failed to get usage for %s", snapshotID)
			}
		}

		if dgst := md.getBlob(); dgst != "" {
			added := make(map[digest.Digest]struct{})
			info, err := cm.ContentStore.Info(ctx, dgst)
			if err == nil {
				usage.Size += info.Size
				added[dgst] = struct{}{}
			}
			walkBlobVariantsOnly(ctx, cm.ContentStore, dgst, func(desc ocispecs.Descriptor) bool {
				if _, ok := added[desc.Digest]; !ok {
					if info, err := cm.ContentStore.Info(ctx, desc.Digest); err == nil {
						usage.Size += info.Size
						added[desc.Digest] = struct{}{}
					}
				}
				return true
			}, nil)
		}

		if err := md.queueSize(usage.Size); err != nil {
			return s, err
		}
		if err := md.commitMetadata(); err != nil {
			return s, err
		}
		return usage.Size, nil
	})
}

// cr must be mutable
func (cr *cacheRecord) hasDirtyVolatile(ctx context.Context) (_ bool, rerr error) {
	if !cr.mutable {
		return false, errors.New("can only check dirty volatile on mutable cache records")
	}
	mntable, err := cr.cm.Snapshotter.Mounts(ctx, cr.md.getSnapshotID())
	if err != nil {
		return false, err
	}
	mnts, cleanup, err := mntable.Mount()
	if err != nil {
		return false, err
	}
	defer cleanup()
	if len(mnts) == 0 {
		return false, nil
	}
	mnt := mnts[0]

	if !overlay.IsOverlayMountType(mnt) {
		return false, nil
	}
	volatileDir := overlay.VolatileIncompatDir(mnt)
	if volatileDir == "" {
		return false, nil
	}
	if _, err := os.Lstat(volatileDir); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// call when holding the manager lock
func (cr *cacheRecord) remove(ctx context.Context) (rerr error) {
	defer func() {
		l := bklog.G(ctx).WithFields(map[string]any{
			"id":    cr.md.ID(),
			"stack": bklog.TraceLevelOnlyStack(),
		})
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("removed cache record")
	}()
	delete(cr.cm.records, cr.md.ID())
	cr.cm.metadataStore.clear(cr.md.ID())
	return nil
}

type immutableRef struct {
	cm *snapshotManager
	refMetadata
	released        bool
	mu              sync.Mutex
	mountCache      snapshot.Mountable
	triggerLastUsed bool
	sizeG           flightcontrol.Group[int64]
}

// hold ref lock before calling
func (sr *immutableRef) traceLogFields() logrus.Fields {
	m := map[string]any{
		"id":       sr.ID(),
		"refID":    fmt.Sprintf("%p", sr),
		"released": sr.released,
		"mutable":  false,
		"stack":    bklog.TraceLevelOnlyStack(),
	}
	return m
}

func (sr *immutableRef) Size(ctx context.Context) (int64, error) {
	return sizeFromMetadata(ctx, &sr.sizeG, sr.cm, sr.md, sr.snapshotID)
}

type mutableRef struct {
	cm *snapshotManager
	refMetadata
	released        bool
	mu              sync.Mutex
	mountCache      snapshot.Mountable
	triggerLastUsed bool
	sizeG           flightcontrol.Group[int64]
}

// hold ref lock before calling
func (sr *mutableRef) traceLogFields() logrus.Fields {
	m := map[string]any{
		"id":       sr.ID(),
		"refID":    fmt.Sprintf("%p", sr),
		"released": sr.released,
		"mutable":  true,
		"stack":    bklog.TraceLevelOnlyStack(),
	}
	return m
}

func (sr *mutableRef) Size(ctx context.Context) (int64, error) {
	return sizeFromMetadata(ctx, &sr.sizeG, sr.cm, sr.md, sr.snapshotID)
}

func (sr *mutableRef) InvalidateSize(_ context.Context) error {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.md.getSize() == sizeUnknown {
		return nil
	}
	if err := sr.md.queueSize(sizeUnknown); err != nil {
		return err
	}
	return sr.md.commitMetadata()
}

// layerToDistributable changes the passed in media type to the "distributable" version of the media type.
func layerToDistributable(mt string) string {
	if !images.IsNonDistributable(mt) {
		// Layer is already a distributable media type (or this is not even a layer).
		// No conversion needed
		return mt
	}

	switch mt {
	case ocispecs.MediaTypeImageLayerNonDistributable: //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
		return ocispecs.MediaTypeImageLayer
	case ocispecs.MediaTypeImageLayerNonDistributableGzip: //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
		return ocispecs.MediaTypeImageLayerGzip
	case ocispecs.MediaTypeImageLayerNonDistributableZstd: //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
		return ocispecs.MediaTypeImageLayerZstd
	case images.MediaTypeDockerSchema2LayerForeign:
		return images.MediaTypeDockerSchema2Layer
	case images.MediaTypeDockerSchema2LayerForeignGzip:
		return images.MediaTypeDockerSchema2LayerGzip
	default:
		return mt
	}
}

func layerToNonDistributable(mt string) string {
	switch mt {
	case ocispecs.MediaTypeImageLayer:
		return ocispecs.MediaTypeImageLayerNonDistributable //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
	case ocispecs.MediaTypeImageLayerGzip:
		return ocispecs.MediaTypeImageLayerNonDistributableGzip //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
	case ocispecs.MediaTypeImageLayerZstd:
		return ocispecs.MediaTypeImageLayerNonDistributableZstd //nolint:staticcheck // ignore SA1019: Non-distributable layers are deprecated, and not recommended for future use.
	case images.MediaTypeDockerSchema2Layer:
		return images.MediaTypeDockerSchema2LayerForeign
	case images.MediaTypeDockerSchema2LayerForeignGzip:
		return images.MediaTypeDockerSchema2LayerForeignGzip
	default:
		return mt
	}
}

func (sr *immutableRef) ociDesc(ctx context.Context, preferNonDist bool) (ocispecs.Descriptor, error) {
	dgst := sr.md.getBlob()
	if dgst == "" {
		return ocispecs.Descriptor{}, errors.Errorf("no blob set for cache record %s", sr.ID())
	}

	desc := ocispecs.Descriptor{
		Digest:      sr.md.getBlob(),
		Size:        sr.md.getBlobSize(),
		Annotations: make(map[string]string),
		MediaType:   sr.md.getMediaType(),
	}

	if preferNonDist {
		if urls := sr.md.getURLs(); len(urls) > 0 {
			// Make sure the media type is the non-distributable version
			// We don't want to rely on the stored media type here because it could have been stored as distributable originally.
			desc.MediaType = layerToNonDistributable(desc.MediaType)
			desc.URLs = urls
		}
	}
	if len(desc.URLs) == 0 {
		// If there are no URL's, there is no reason to have this be non-dsitributable
		desc.MediaType = layerToDistributable(desc.MediaType)
	}

	if blobDesc, err := getBlobDesc(ctx, sr.cm.ContentStore, desc.Digest); err == nil {
		if blobDesc.Annotations != nil {
			desc.Annotations = blobDesc.Annotations
		}
	}

	diffID := sr.md.getDiffID()
	if diffID != "" {
		desc.Annotations[labels.LabelUncompressed] = string(diffID)
	}

	createdAt := sr.GetCreatedAt()
	if !createdAt.IsZero() {
		createdAt, err := createdAt.MarshalText()
		if err != nil {
			return ocispecs.Descriptor{}, err
		}
		desc.Annotations["buildkit/createdat"] = string(createdAt)
	}

	return desc, nil
}

const (
	blobVariantGCLabel         = "containerd.io/gc.ref.content.blob-"
	blobAnnotationsLabelPrefix = "buildkit.io/blob/annotation."
	blobMediaTypeLabel         = "buildkit.io/blob/mediatype"
)

// linkBlob makes a link between this ref and the passed blob. The linked blob can be
// acquired during walkBlob. This is useful to associate a compression variant blob to
// this ref. This doesn't record the blob to the cache record (i.e. the passed blob can't
// be acquired through getBlob). Use setBlob for that purpose.
func (sr *immutableRef) linkBlob(ctx context.Context, desc ocispecs.Descriptor) error {
	cs := sr.cm.ContentStore
	blobDigest := sr.md.getBlob()
	info, err := cs.Info(ctx, blobDigest)
	if err != nil {
		return err
	}
	vInfo, err := cs.Info(ctx, desc.Digest)
	if err != nil {
		return err
	}
	vInfo.Labels = map[string]string{
		blobVariantGCLabel + blobDigest.String(): blobDigest.String(),
	}
	vInfo = addBlobDescToInfo(desc, vInfo)
	if _, err := cs.Update(ctx, vInfo, fieldsFromLabels(vInfo.Labels)...); err != nil {
		return err
	}
	// let the future call to size() recalcultate the new size
	sr.mu.Lock()
	if err := sr.md.queueSize(sizeUnknown); err != nil {
		sr.mu.Unlock()
		return err
	}
	if err := sr.md.commitMetadata(); err != nil {
		sr.mu.Unlock()
		return err
	}
	sr.mu.Unlock()
	if err := sr.cm.recordSnapshotContent(sr.SnapshotID(), desc); err != nil {
		return err
	}
	if desc.Digest == blobDigest {
		return nil
	}
	info.Labels = map[string]string{
		blobVariantGCLabel + desc.Digest.String(): desc.Digest.String(),
	}
	_, err = cs.Update(ctx, info, fieldsFromLabels(info.Labels)...)
	return err
}

func (sr *immutableRef) getBlobWithCompression(ctx context.Context, compressionType compression.Type) (ocispecs.Descriptor, error) {
	if _, err := sr.cm.ContentStore.Info(ctx, sr.md.getBlob()); err != nil {
		return ocispecs.Descriptor{}, err
	}
	desc, err := sr.ociDesc(ctx, true)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	return getBlobWithCompression(ctx, sr.cm.ContentStore, desc, compressionType)
}

func getBlobWithCompression(ctx context.Context, cs content.Store, desc ocispecs.Descriptor, compressionType compression.Type) (ocispecs.Descriptor, error) {
	var target *ocispecs.Descriptor
	if err := walkBlob(ctx, cs, desc, func(desc ocispecs.Descriptor) bool {
		if needs, err := compressionType.NeedsConversion(ctx, cs, desc); err == nil && !needs {
			target = &desc
			return false
		}
		return true
	}); err != nil || target == nil {
		return ocispecs.Descriptor{}, cerrdefs.ErrNotFound
	}
	return *target, nil
}

func walkBlob(ctx context.Context, cs content.Store, desc ocispecs.Descriptor, f func(ocispecs.Descriptor) bool) error {
	if !f(desc) {
		return nil
	}
	if _, err := walkBlobVariantsOnly(ctx, cs, desc.Digest, func(desc ocispecs.Descriptor) bool { return f(desc) }, nil); err != nil {
		return err
	}
	return nil
}

func walkBlobVariantsOnly(ctx context.Context, cs content.Store, dgst digest.Digest, f func(ocispecs.Descriptor) bool, visited map[digest.Digest]struct{}) (bool, error) {
	if visited == nil {
		visited = make(map[digest.Digest]struct{})
	}
	visited[dgst] = struct{}{}
	info, err := cs.Info(ctx, dgst)
	if errors.Is(err, cerrdefs.ErrNotFound) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	var children []digest.Digest
	for k, dgstS := range info.Labels {
		if !strings.HasPrefix(k, blobVariantGCLabel) {
			continue
		}
		cDgst, err := digest.Parse(dgstS)
		if err != nil || cDgst == dgst {
			continue
		}
		if cDesc, err := getBlobDesc(ctx, cs, cDgst); err == nil {
			if !f(cDesc) {
				return false, nil
			}
		}
		children = append(children, cDgst)
	}
	for _, c := range children {
		if _, isVisited := visited[c]; isVisited {
			continue
		}
		if isContinue, err := walkBlobVariantsOnly(ctx, cs, c, f, visited); !isContinue || err != nil {
			return isContinue, err
		}
	}
	return true, nil
}

func getBlobDesc(ctx context.Context, cs content.Store, dgst digest.Digest) (ocispecs.Descriptor, error) {
	info, err := cs.Info(ctx, dgst)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	if info.Labels == nil {
		return ocispecs.Descriptor{}, errors.Errorf("no blob metadata is stored for %q", info.Digest)
	}
	mt, ok := info.Labels[blobMediaTypeLabel]
	if !ok {
		return ocispecs.Descriptor{}, errors.Errorf("no media type is stored for %q", info.Digest)
	}
	desc := ocispecs.Descriptor{
		Digest:    info.Digest,
		Size:      info.Size,
		MediaType: mt,
	}
	for k, v := range info.Labels {
		if strings.HasPrefix(k, blobAnnotationsLabelPrefix) {
			if desc.Annotations == nil {
				desc.Annotations = make(map[string]string)
			}
			desc.Annotations[strings.TrimPrefix(k, blobAnnotationsLabelPrefix)] = v
		}
	}
	if len(desc.URLs) == 0 {
		// If there are no URL's, there is no reason to have this be non-dsitributable
		desc.MediaType = layerToDistributable(desc.MediaType)
	}
	return desc, nil
}

func addBlobDescToInfo(desc ocispecs.Descriptor, info content.Info) content.Info {
	if _, ok := info.Labels[blobMediaTypeLabel]; ok {
		return info // descriptor information already stored
	}
	if info.Labels == nil {
		info.Labels = make(map[string]string)
	}
	info.Labels[blobMediaTypeLabel] = desc.MediaType
	for k, v := range filterAnnotationsForSave(desc.Annotations) {
		info.Labels[blobAnnotationsLabelPrefix+k] = v
	}
	return info
}

func filterAnnotationsForSave(a map[string]string) (b map[string]string) {
	if a == nil {
		return nil
	}
	for _, k := range additionalAnnotations {
		v, ok := a[k]
		if !ok {
			continue
		}
		if b == nil {
			b = make(map[string]string)
		}
		b[k] = v
	}
	return
}

func fieldsFromLabels(labels map[string]string) (fields []string) {
	for k := range labels {
		fields = append(fields, "labels."+k)
	}
	return
}

func (sr *immutableRef) Mount(ctx context.Context, readonly bool) (_ snapshot.Mountable, rerr error) {
	if sr.released {
		return nil, errors.Wrapf(errInvalid, "invalid immutable ref %p", sr)
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()

	viewLeaseID := identity.NewID()
	viewSnapshotID := viewLeaseID + "-view"
	if _, err := sr.cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = viewLeaseID
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	}, leaseutil.MakeTemporary); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return nil, err
	}
	releaseViewLease := func() error {
		err := sr.cm.LeaseManager.Delete(context.TODO(), leases.Lease{ID: viewLeaseID})
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := sr.cm.LeaseManager.AddResource(ctx, leases.Lease{ID: viewLeaseID}, leases.Resource{
		ID:   viewSnapshotID,
		Type: "snapshots/" + sr.cm.Snapshotter.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		_ = releaseViewLease()
		return nil, err
	}
	mnts, err := sr.cm.Snapshotter.View(ctx, viewSnapshotID, sr.SnapshotID())
	if err != nil && !cerrdefs.IsAlreadyExists(err) {
		_ = releaseViewLease()
		return nil, err
	}
	if readonly {
		mnts = setReadonly(mnts)
	}
	return &mountableWithRelease{
		Mountable: mnts,
		release:   releaseViewLease,
	}, nil
}

func (sr *immutableRef) Release(ctx context.Context) error {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	return sr.release(ctx)
}

func (sr *immutableRef) shouldUpdateLastUsed() bool {
	return sr.triggerLastUsed
}

func (sr *immutableRef) updateLastUsedNow() bool {
	return sr.triggerLastUsed
}

func (sr *immutableRef) release(ctx context.Context) (rerr error) {
	defer func() {
		l := bklog.G(ctx).WithFields(sr.traceLogFields())
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("released cache ref")
	}()
	if sr.released {
		return nil
	}
	if sr.updateLastUsedNow() {
		if err := sr.md.updateLastUsed(); err != nil {
			return err
		}
	}
	sr.mountCache = nil
	sr.released = true
	return nil
}

func (sr *mutableRef) shouldUpdateLastUsed() bool {
	return sr.triggerLastUsed
}

func (sr *mutableRef) commit(ctx context.Context) (_ *immutableRef, rerr error) {
	if sr.released {
		return nil, errors.Wrapf(errInvalid, "invalid mutable ref %p", sr)
	}
	rec, ok := sr.cm.records[sr.snapshotID]
	if !ok || !rec.mutable || rec.locked == false {
		return nil, errors.Wrapf(errInvalid, "invalid mutable ref %p", sr)
	}

	id := identity.NewID()
	md := sr.cm.ensureMetadata(id)
	committed := &cacheRecord{cm: sr.cm, md: md}
	if err := sr.cm.Snapshotter.Commit(ctx, id, sr.SnapshotID()); err != nil {
		return nil, errors.Wrapf(err, "failed to commit %s to immutable %s", sr.SnapshotID(), id)
	}

	if descr := sr.GetDescription(); descr != "" {
		if err := md.queueDescription(descr); err != nil {
			return nil, err
		}
	}
	if layerType := sr.GetLayerType(); layerType != "" {
		if err := md.SetLayerType(layerType); err != nil {
			return nil, err
		}
	}
	if recordType := sr.GetRecordType(); recordType != "" {
		if err := md.SetRecordType(recordType); err != nil {
			return nil, err
		}
	}
	if createdAt := sr.GetCreatedAt(); !createdAt.IsZero() {
		if err := md.SetCreatedAt(createdAt); err != nil {
			return nil, err
		}
	}
	for _, imageRef := range sr.md.getImageRefs() {
		if err := md.appendImageRef(imageRef); err != nil {
			return nil, err
		}
	}

	if err := initializeMetadata(committed.md); err != nil {
		return nil, err
	}

	sr.cm.records[id] = committed

	if err := md.queueCommitted(true); err != nil {
		return nil, err
	}
	if err := md.queueSize(sizeUnknown); err != nil {
		return nil, err
	}
	if err := md.queueSnapshotID(id); err != nil {
		return nil, err
	}
	if err := md.commitMetadata(); err != nil {
		return nil, err
	}

	if err := rec.remove(context.WithoutCancel(ctx)); err != nil {
		return nil, err
	}
	sr.mountCache = nil
	sr.released = true

	ref := &immutableRef{
		cm:              sr.cm,
		refMetadata:     refMetadata{snapshotID: committed.md.getSnapshotID(), md: committed.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}

func (sr *mutableRef) Mount(ctx context.Context, readonly bool) (_ snapshot.Mountable, rerr error) {
	if sr.released {
		return nil, errors.Wrapf(errInvalid, "invalid mutable ref %p", sr)
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.mutableMount(ctx, readonly)
}

func (sr *mutableRef) mutableMount(ctx context.Context, readonly bool) (_ snapshot.Mountable, rerr error) {
	if sr.mountCache == nil {
		mnt, err := sr.cm.Snapshotter.Mounts(ctx, sr.SnapshotID())
		if err != nil {
			return nil, err
		}
		if sr.GetRecordType() == client.UsageRecordTypeCacheMount {
			mnt = sr.cm.mountPool.setSharable(mnt)
		}
		sr.mountCache = mnt
	}
	if readonly {
		return setReadonly(sr.mountCache), nil
	}
	return sr.mountCache, nil
}

func (sr *mutableRef) Commit(ctx context.Context) (ImmutableRef, error) {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	return sr.commit(ctx)
}

func (sr *mutableRef) Release(ctx context.Context) error {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	return sr.release(ctx)
}

func (sr *mutableRef) release(ctx context.Context) (rerr error) {
	if sr.released {
		return nil
	}
	rec, ok := sr.cm.records[sr.snapshotID]
	if !ok || !rec.mutable {
		return errors.Wrapf(errInvalid, "invalid mutable ref %p", sr)
	}
	defer func() {
		l := bklog.G(ctx).WithFields(sr.traceLogFields())
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("released cache ref")
	}()
	sr.mountCache = nil
	sr.released = true
	rec.locked = false
	return rec.remove(ctx)
}

func setReadonly(mounts snapshot.Mountable) snapshot.Mountable {
	return &readOnlyMounter{mounts}
}

type readOnlyMounter struct {
	snapshot.Mountable
}

type mountableWithRelease struct {
	snapshot.Mountable
	release func() error
}

func (m *mountableWithRelease) Mount() ([]mount.Mount, func() error, error) {
	mounts, release, err := m.Mountable.Mount()
	if err != nil {
		if m.release != nil {
			_ = m.release()
		}
		return nil, nil, err
	}
	return mounts, func() error {
		var retErr error
		if release != nil {
			retErr = release()
		}
		if m.release != nil {
			if err := m.release(); err != nil && retErr == nil {
				retErr = err
			}
		}
		return retErr
	}, nil
}

func (m *readOnlyMounter) Mount() ([]mount.Mount, func() error, error) {
	mounts, release, err := m.Mountable.Mount()
	if err != nil {
		return nil, nil, err
	}
	for i, m := range mounts {
		if overlay.IsOverlayMountType(m) {
			mounts[i].Options = readonlyOverlay(m.Options)
			continue
		}
		opts := make([]string, 0, len(m.Options))
		for _, opt := range m.Options {
			if opt != "rw" {
				opts = append(opts, opt)
			}
		}
		opts = append(opts, "ro")
		mounts[i].Options = opts
	}
	return mounts, release, nil
}

func readonlyOverlay(opt []string) []string {
	out := make([]string, 0, len(opt))
	upper := ""
	for _, o := range opt {
		if strings.HasPrefix(o, "upperdir=") {
			upper = strings.TrimPrefix(o, "upperdir=")
		} else if strings.HasPrefix(o, "workdir=") || o == "volatile" {
			continue
		} else {
			out = append(out, o)
		}
	}
	if upper != "" {
		for i, o := range out {
			if strings.HasPrefix(o, "lowerdir=") {
				out[i] = "lowerdir=" + upper + ":" + strings.TrimPrefix(o, "lowerdir=")
			}
		}
	}
	return out
}

func newSharableMountPool(tmpdirRoot string) (sharableMountPool, error) {
	if tmpdirRoot != "" {
		if err := os.MkdirAll(tmpdirRoot, 0700); err != nil {
			return sharableMountPool{}, errors.Wrap(err, "failed to prepare mount pool")
		}
		// If tmpdirRoot is specified, remove existing mounts to avoid conflict.
		files, err := os.ReadDir(tmpdirRoot)
		if err != nil {
			return sharableMountPool{}, errors.Wrap(err, "failed to read mount pool")
		}
		for _, file := range files {
			if file.IsDir() {
				dir := filepath.Join(tmpdirRoot, file.Name())
				bklog.G(context.Background()).Debugf("cleaning up existing temporary mount %q", dir)
				if err := mount.Unmount(dir, 0); err != nil {
					if mounted, merr := mountinfo.Mounted(dir); merr != nil || mounted {
						bklog.G(context.Background()).WithError(err).WithError(merr).
							WithField("mounted", mounted).Warnf("failed to unmount existing temporary mount %q", dir)
						continue
					}
				}
				if err := os.Remove(dir); err != nil {
					bklog.G(context.Background()).WithError(err).Warnf("failed to remove existing temporary mount %q", dir)
				}
			}
		}
	}
	return sharableMountPool{tmpdirRoot}, nil
}

type sharableMountPool struct {
	tmpdirRoot string
}

func (p sharableMountPool) setSharable(mounts snapshot.Mountable) snapshot.Mountable {
	return &sharableMountable{Mountable: mounts, mountPoolRoot: p.tmpdirRoot}
}

// sharableMountable allows sharing underlying (possibly writable) mounts among callers.
// This is useful to share writable overlayfs mounts.
//
// NOTE: Mount() method doesn't return the underlying mount configuration (e.g. overlayfs mounts)
//
//	instead it always return bind mounts of the temporary mount point. So if the caller
//	needs to inspect the underlying mount configuration (e.g. for optimized differ for
//	overlayfs), this wrapper shouldn't be used.
type sharableMountable struct {
	snapshot.Mountable

	count         int32
	mu            sync.Mutex
	mountPoolRoot string

	curMounts              []mount.Mount
	curMountPoint          string
	curRelease             func() error
	curOverlayIncompatDirs []string
}

func (sm *sharableMountable) Mount() (_ []mount.Mount, _ func() error, retErr error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.curMounts == nil {
		mounts, release, err := sm.Mountable.Mount()
		if err != nil {
			return nil, nil, err
		}
		defer func() {
			if retErr != nil {
				release()
			}
		}()
		var isOverlay bool
		for _, m := range mounts {
			if overlay.IsOverlayMountType(m) {
				isOverlay = true
				break
			}
		}
		if !isOverlay {
			// Don't need temporary mount wrapper for non-overlayfs mounts
			return mounts, release, nil
		}
		dir, err := os.MkdirTemp(sm.mountPoolRoot, "buildkit")
		if err != nil {
			return nil, nil, err
		}
		defer func() {
			if retErr != nil {
				os.Remove(dir)
			}
		}()
		if userns.RunningInUserNS() {
			mounts, err = rootlessmountopts.FixUp(mounts)
			if err != nil {
				return nil, nil, err
			}
		}
		if err := mount.All(mounts, dir); err != nil {
			return nil, nil, err
		}
		overlayIncompatDirs := overlay.VolatileIncompatDirs(mounts)

		defer func() {
			if retErr != nil {
				mount.Unmount(dir, 0)
				for _, dir := range overlayIncompatDirs {
					os.RemoveAll(dir)
				}
			}
		}()
		sm.curMounts = []mount.Mount{
			{
				Source: dir,
				Type:   "bind",
				Options: []string{
					"rw",
					"rbind",
				},
			},
		}
		sm.curMountPoint = dir
		sm.curRelease = release
		sm.curOverlayIncompatDirs = overlayIncompatDirs
	}

	mounts := make([]mount.Mount, len(sm.curMounts))
	copy(mounts, sm.curMounts)

	sm.count++
	return mounts, func() error {
		sm.mu.Lock()
		defer sm.mu.Unlock()

		sm.count--
		if sm.count < 0 {
			return fmt.Errorf("release of released mount %s", sm.curMountPoint)
		} else if sm.count > 0 {
			return nil
		}

		// no mount exist. release the current mount.
		sm.curMounts = nil
		if err := mount.Unmount(sm.curMountPoint, 0); err != nil {
			slog.Error("failed to unmount sharable mount", "err", err)
			return err
		}
		for _, dir := range sm.curOverlayIncompatDirs {
			os.RemoveAll(dir)
		}
		sm.curOverlayIncompatDirs = nil
		if err := sm.curRelease(); err != nil {
			return err
		}
		return os.Remove(sm.curMountPoint)
	}, nil
}
