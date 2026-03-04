package snapshots

import (
	"context"
	"fmt"
	"maps"
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
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/overlay"
	"github.com/dagger/dagger/internal/buildkit/util/progress"
	rootlessmountopts "github.com/dagger/dagger/internal/buildkit/util/rootless/mountopts"
	"github.com/dagger/dagger/internal/buildkit/util/winlayers"
	"github.com/docker/docker/pkg/idtools"
	"github.com/hashicorp/go-multierror"
	"github.com/moby/sys/mountinfo"
	"github.com/moby/sys/userns"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var additionalAnnotations = append(compression.EStargzAnnotations, labels.LabelUncompressed)

// Ref is a reference to cacheable objects.
type Ref interface {
	Mountable
	RefMetadata
	Release(context.Context) error
	IdentityMapping() *idtools.IdentityMapping
	DescHandler(digest.Digest) *DescHandler
}

type ImmutableRef interface {
	Ref
	Clone() ImmutableRef
	// Finalize commits the snapshot to the driver if it's not already.
	// This means the snapshot can no longer be mounted as mutable.
	Finalize(context.Context) error

	Extract(ctx context.Context, s session.Group) error // +progress
	// TODO: temporary compatibility seam for exporter paths.
	// Move remote descriptor construction to exporter-side code and drop this from snapshots.
	GetRemotes(ctx context.Context, createIfNeeded bool, cfg config.RefConfig, all bool, s session.Group) ([]*Remote, error)
	LayerChain() RefList
	FileList(ctx context.Context, s session.Group) ([]string, error)
}

type MutableRef interface {
	Ref
	Commit(context.Context) (ImmutableRef, error)
}

type Mountable interface {
	Mount(ctx context.Context, readonly bool, s session.Group) (snapshot.Mountable, error)
}

type ref interface {
	shouldUpdateLastUsed() bool
}

type cacheRecord struct {
	cm *snapshotManager
	mu *sync.Mutex // the mutex is shared by records sharing data

	mutable bool
	refs    map[ref]struct{}
	parentRefs
	*cacheMetadata

	// dead means record is marked as deleted
	dead bool

	mountCache snapshot.Mountable

	sizeG flightcontrol.Group[int64]

	// these are filled if multiple refs point to same data
	equalMutable   *mutableRef
	equalImmutable *immutableRef

	layerDigestChainCache *digestChain
}

// hold ref lock before calling
func (cr *cacheRecord) ref(triggerLastUsed bool, descHandlers DescHandlers) *immutableRef {
	ref := &immutableRef{
		cacheRecord:     cr,
		triggerLastUsed: triggerLastUsed,
		descHandlers:    descHandlers,
	}
	cr.refs[ref] = struct{}{}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref
}

// hold ref lock before calling
func (cr *cacheRecord) mref(triggerLastUsed bool, descHandlers DescHandlers) *mutableRef {
	ref := &mutableRef{
		cacheRecord:     cr,
		triggerLastUsed: triggerLastUsed,
		descHandlers:    descHandlers,
	}
	cr.refs[ref] = struct{}{}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref
}

// parentRefs is a disjoint union type that holds either a single layerParent for this record, a list
// of parents if this is a merged record or all nil fields if this record has no parents. At most one
// field should be non-nil at a time.
type parentRefs struct {
	layerParent  *immutableRef
	mergeParents []*immutableRef
	diffParents  *diffParents
}

type diffParents struct {
	lower *immutableRef
	upper *immutableRef
}

// caller must hold snapshotManager.mu
func (p parentRefs) release(ctx context.Context) (rerr error) {
	_ = ctx
	return nil
}

func (p parentRefs) clone() parentRefs {
	if len(p.mergeParents) > 0 {
		newParents := make([]*immutableRef, len(p.mergeParents))
		for i, p := range p.mergeParents {
			newParents[i] = p
		}
		p.mergeParents = newParents
	}
	if p.diffParents != nil {
		p.diffParents = &diffParents{
			lower: p.diffParents.lower,
			upper: p.diffParents.upper,
		}
	}
	return p
}

type refKind int

const (
	BaseLayer refKind = iota
	Layer
	Merge
	Diff
)

func (k refKind) String() string {
	switch k {
	case BaseLayer:
		return "base"
	case Layer:
		return "layer"
	case Merge:
		return "merge"
	case Diff:
		return "diff"
	}
	return "unknown"
}

func (cr *cacheRecord) kind() refKind {
	if len(cr.mergeParents) > 0 {
		return Merge
	}
	if cr.diffParents != nil {
		return Diff
	}
	if cr.layerParent != nil {
		return Layer
	}
	return BaseLayer
}

// hold ref lock before calling
func (cr *cacheRecord) isDead() bool {
	return cr.dead || (cr.equalImmutable != nil && cr.equalImmutable.dead) || (cr.equalMutable != nil && cr.equalMutable.dead)
}

var errSkipWalk = errors.New("skip")

// walkAncestors calls the provided func on cr and each of its ancestors, counting layer,
// diff, and merge parents. It starts at cr and does a depth-first walk to parents. It will visit
// a record and its parents multiple times if encountered more than once. It will only skip
// visiting parents of a record if errSkipWalk is returned. If any other error is returned,
// the walk will stop and return the error to the caller.
func (cr *cacheRecord) walkAncestors(f func(*cacheRecord) error) error {
	curs := []*cacheRecord{cr}
	for len(curs) > 0 {
		cur := curs[len(curs)-1]
		curs = curs[:len(curs)-1]
		if err := f(cur); err != nil {
			if errors.Is(err, errSkipWalk) {
				continue
			}
			return err
		}
		switch cur.kind() {
		case Layer:
			curs = append(curs, cur.layerParent.cacheRecord)
		case Merge:
			for _, p := range cur.mergeParents {
				curs = append(curs, p.cacheRecord)
			}
		case Diff:
			if cur.diffParents.lower != nil {
				curs = append(curs, cur.diffParents.lower.cacheRecord)
			}
			if cur.diffParents.upper != nil {
				curs = append(curs, cur.diffParents.upper.cacheRecord)
			}
		}
	}
	return nil
}

// walkUniqueAncestors calls walkAncestors but skips a record if it's already been visited.
func (cr *cacheRecord) walkUniqueAncestors(f func(*cacheRecord) error) error {
	memo := make(map[*cacheRecord]struct{})
	return cr.walkAncestors(func(cr *cacheRecord) error {
		if _, ok := memo[cr]; ok {
			return errSkipWalk
		}
		memo[cr] = struct{}{}
		return f(cr)
	})
}

func (cr *cacheRecord) isLazy(ctx context.Context) (bool, error) {
	_ = ctx
	return false, nil
}

func (cr *cacheRecord) IdentityMapping() *idtools.IdentityMapping {
	return cr.cm.IdentityMapping()
}

func (cr *cacheRecord) viewLeaseID() string {
	return cr.ID() + "-view"
}

func (cr *cacheRecord) compressionVariantsLeaseID() string {
	return cr.ID() + "-variants"
}

func (cr *cacheRecord) viewSnapshotID() string {
	return cr.getSnapshotID() + "-view"
}

func (cr *cacheRecord) size(ctx context.Context) (int64, error) {
	// this expects that usage() is implemented lazily
	return cr.sizeG.Do(ctx, cr.ID(), func(ctx context.Context) (int64, error) {
		cr.mu.Lock()
		s := cr.getSize()
		if s != sizeUnknown {
			cr.mu.Unlock()
			return s, nil
		}
		driverID := cr.getSnapshotID()
		if cr.equalMutable != nil {
			driverID = cr.equalMutable.getSnapshotID()
		}
		cr.mu.Unlock()
		var usage snapshots.Usage
		if !cr.getBlobOnly() {
			var err error
			usage, err = cr.cm.Snapshotter.Usage(ctx, driverID)
			if err != nil {
				cr.mu.Lock()
				isDead := cr.isDead()
				cr.mu.Unlock()
				if isDead {
					return 0, nil
				}
				if !errors.Is(err, cerrdefs.ErrNotFound) {
					return s, errors.Wrapf(err, "failed to get usage for %s", cr.ID())
				}
			}
		}
		if dgst := cr.getBlob(); dgst != "" {
			added := make(map[digest.Digest]struct{})
			info, err := cr.cm.ContentStore.Info(ctx, digest.Digest(dgst))
			if err == nil {
				usage.Size += info.Size
				added[digest.Digest(dgst)] = struct{}{}
			}
			walkBlobVariantsOnly(ctx, cr.cm.ContentStore, digest.Digest(dgst), func(desc ocispecs.Descriptor) bool {
				if _, ok := added[desc.Digest]; !ok {
					if info, err := cr.cm.ContentStore.Info(ctx, desc.Digest); err == nil {
						usage.Size += info.Size
						added[desc.Digest] = struct{}{}
					}
				}
				return true
			}, nil)
		}
		cr.mu.Lock()
		cr.queueSize(usage.Size)
		if err := cr.commitMetadata(); err != nil {
			cr.mu.Unlock()
			return s, err
		}
		cr.mu.Unlock()
		return usage.Size, nil
	})
}

// caller must hold cr.mu
func (cr *cacheRecord) mount(ctx context.Context) (_ snapshot.Mountable, rerr error) {
	if cr.mountCache != nil {
		return cr.mountCache, nil
	}

	var mountSnapshotID string
	if cr.mutable {
		mountSnapshotID = cr.getSnapshotID()
	} else if cr.equalMutable != nil {
		mountSnapshotID = cr.equalMutable.getSnapshotID()
	} else {
		mountSnapshotID = cr.viewSnapshotID()
		if _, err := cr.cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
			l.ID = cr.viewLeaseID()
			l.Labels = map[string]string{
				"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
			}
			return nil
		}, leaseutil.MakeTemporary); err != nil && !cerrdefs.IsAlreadyExists(err) {
			return nil, err
		}
		defer func() {
			if rerr != nil {
				cr.cm.LeaseManager.Delete(context.TODO(), leases.Lease{ID: cr.viewLeaseID()})
			}
		}()
		if err := cr.cm.LeaseManager.AddResource(ctx, leases.Lease{ID: cr.viewLeaseID()}, leases.Resource{
			ID:   mountSnapshotID,
			Type: "snapshots/" + cr.cm.Snapshotter.Name(),
		}); err != nil && !cerrdefs.IsAlreadyExists(err) {
			return nil, err
		}
		// Return the mount direct from View rather than setting it using the Mounts call below.
		// The two are equivalent for containerd snapshotters but the moby snapshotter requires
		// the use of the mountable returned by View in this case.
		mnts, err := cr.cm.Snapshotter.View(ctx, mountSnapshotID, cr.getSnapshotID())
		if err != nil && !cerrdefs.IsAlreadyExists(err) {
			return nil, err
		}
		cr.mountCache = mnts
	}

	if cr.mountCache != nil {
		return cr.mountCache, nil
	}

	mnts, err := cr.cm.Snapshotter.Mounts(ctx, mountSnapshotID)
	if err != nil {
		return nil, err
	}
	cr.mountCache = mnts
	return cr.mountCache, nil
}

// cr must be mutable
func (cr *cacheRecord) hasDirtyVolatile(ctx context.Context) (_ bool, rerr error) {
	if !cr.mutable {
		return false, errors.New("can only check dirty volatile on mutable cache records")
	}
	mntable, err := cr.mutableMount(ctx, false, nil)
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
func (cr *cacheRecord) remove(ctx context.Context, removeSnapshot bool) (rerr error) {
	defer func() {
		l := bklog.G(ctx).WithFields(map[string]any{
			"id":             cr.ID(),
			"refCount":       len(cr.refs),
			"removeSnapshot": removeSnapshot,
			"stack":          bklog.TraceLevelOnlyStack(),
		})
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("removed cache record")
	}()
	delete(cr.cm.records, cr.ID())
	if removeSnapshot {
		if err := cr.cm.LeaseManager.Delete(ctx, leases.Lease{
			ID: cr.ID(),
		}); err != nil && !cerrdefs.IsNotFound(err) {
			return errors.Wrapf(err, "failed to delete lease for %s", cr.ID())
		}
		if err := cr.cm.LeaseManager.Delete(ctx, leases.Lease{
			ID: cr.compressionVariantsLeaseID(),
		}); err != nil && !cerrdefs.IsNotFound(err) {
			return errors.Wrapf(err, "failed to delete compression variant lease for %s", cr.ID())
		}
	}
	cr.cm.metadataStore.clear(cr.ID())
	if err := cr.parentRefs.release(ctx); err != nil {
		return errors.Wrapf(err, "failed to release parents of %s", cr.ID())
	}
	return nil
}

type immutableRef struct {
	*cacheRecord
	triggerLastUsed bool
	descHandlers    DescHandlers
}

// hold ref lock before calling
func (sr *immutableRef) traceLogFields() logrus.Fields {
	m := map[string]any{
		"id":          sr.ID(),
		"refID":       fmt.Sprintf("%p", sr),
		"newRefCount": len(sr.refs),
		"mutable":     false,
		"stack":       bklog.TraceLevelOnlyStack(),
	}
	if sr.equalMutable != nil {
		m["equalMutableID"] = sr.equalMutable.ID()
	}
	if sr.equalImmutable != nil {
		m["equalImmutableID"] = sr.equalImmutable.ID()
	}
	return m
}

// Order is from parent->child, sr will be at end of slice. Refs should not
// be released as they are used internally in the underlying cacheRecords.
func (sr *immutableRef) layerChain() []*immutableRef {
	var count int
	sr.layerWalk(func(*immutableRef) {
		count++
	})
	layers := make([]*immutableRef, count)
	var index int
	sr.layerWalk(func(sr *immutableRef) {
		layers[index] = sr
		index++
	})
	return layers
}

// returns the set of cache record IDs for each layer in sr's layer chain
func (sr *immutableRef) layerSet() map[string]struct{} {
	var count int
	sr.layerWalk(func(*immutableRef) {
		count++
	})
	set := make(map[string]struct{}, count)
	sr.layerWalk(func(sr *immutableRef) {
		set[sr.ID()] = struct{}{}
	})
	return set
}

// layerWalk visits each ref representing an actual layer in the chain for
// sr (including sr). The layers are visited from lowest->highest as ordered
// in the remote for the ref.
func (sr *immutableRef) layerWalk(f func(*immutableRef)) {
	switch sr.kind() {
	case Merge:
		for _, parent := range sr.mergeParents {
			parent.layerWalk(f)
		}
	case Diff:
		lower := sr.diffParents.lower
		upper := sr.diffParents.upper
		// If upper is only one blob different from lower, then re-use that blob
		switch {
		case upper != nil && lower == nil && upper.kind() == BaseLayer:
			// upper is a single layer being diffed with scratch
			f(upper)
		case upper != nil && lower != nil && upper.kind() == Layer && upper.layerParent.ID() == lower.ID():
			// upper is a single layer on top of lower
			f(upper)
		default:
			// otherwise, the diff will be computed and turned into its own single blob
			f(sr)
		}
	case Layer:
		sr.layerParent.layerWalk(f)
		fallthrough
	case BaseLayer:
		f(sr)
	}
}

type digestChain struct {
	digests []digest.Digest
	seen    map[digest.Digest]struct{}
}

func (chain *digestChain) add(dig digest.Digest) {
	if _, exists := chain.seen[dig]; !exists {
		chain.digests = append(chain.digests, dig)
		chain.seen[dig] = struct{}{}
	}
}

// hold cacheRecord.mu lock before calling
func (cr *cacheRecord) layerDigestChain() *digestChain {
	if cr.layerDigestChainCache != nil {
		return cr.layerDigestChainCache
	}
	cr.layerDigestChainCache = &digestChain{seen: map[digest.Digest]struct{}{}}
	switch cr.kind() {
	case Diff:
		if cr.getBlob() == "" && cr.diffParents.upper != nil {
			// this diff just reuses the upper blob
			cr.layerDigestChainCache = cr.diffParents.upper.layerDigestChain()
		} else {
			cr.layerDigestChainCache.add(cr.getBlob())
		}
	case Merge:
		for _, parent := range cr.mergeParents {
			for _, dig := range parent.layerDigestChain().digests {
				cr.layerDigestChainCache.add(dig)
			}
		}
	case Layer:
		for _, dig := range cr.layerParent.layerDigestChain().digests {
			cr.layerDigestChainCache.add(dig)
		}
		fallthrough
	case BaseLayer:
		cr.layerDigestChainCache.add(cr.getBlob())
	}
	return cr.layerDigestChainCache
}

type RefList []ImmutableRef

func (l RefList) Release(ctx context.Context) (rerr error) {
	for i, r := range l {
		if r == nil {
			continue
		}
		if err := r.Release(ctx); err != nil {
			rerr = multierror.Append(rerr, err).ErrorOrNil()
		} else {
			l[i] = nil
		}
	}
	return rerr
}

func (sr *immutableRef) LayerChain() RefList {
	chain := sr.layerChain()
	l := RefList(make([]ImmutableRef, len(chain)))
	for i, p := range chain {
		l[i] = p.Clone()
	}
	return l
}

func (sr *immutableRef) DescHandler(dgst digest.Digest) *DescHandler {
	return sr.descHandlers[dgst]
}

type mutableRef struct {
	*cacheRecord
	triggerLastUsed bool
	descHandlers    DescHandlers
}

// hold ref lock before calling
func (sr *mutableRef) traceLogFields() logrus.Fields {
	m := map[string]any{
		"id":          sr.ID(),
		"refID":       fmt.Sprintf("%p", sr),
		"newRefCount": len(sr.refs),
		"mutable":     true,
		"stack":       bklog.TraceLevelOnlyStack(),
	}
	if sr.equalMutable != nil {
		m["equalMutableID"] = sr.equalMutable.ID()
	}
	if sr.equalImmutable != nil {
		m["equalImmutableID"] = sr.equalImmutable.ID()
	}
	return m
}

func (sr *mutableRef) DescHandler(dgst digest.Digest) *DescHandler {
	return sr.descHandlers[dgst]
}

func (sr *immutableRef) clone() *immutableRef {
	sr.mu.Lock()
	ref := sr.ref(false, sr.descHandlers)
	sr.mu.Unlock()
	return ref
}

func (sr *immutableRef) Clone() ImmutableRef {
	return sr.clone()
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

func (sr *immutableRef) ociDesc(ctx context.Context, dhs DescHandlers, preferNonDist bool) (ocispecs.Descriptor, error) {
	dgst := sr.getBlob()
	if dgst == "" {
		return ocispecs.Descriptor{}, errors.Errorf("no blob set for cache record %s", sr.ID())
	}

	desc := ocispecs.Descriptor{
		Digest:      sr.getBlob(),
		Size:        sr.getBlobSize(),
		Annotations: make(map[string]string),
		MediaType:   sr.getMediaType(),
	}

	if preferNonDist {
		if urls := sr.getURLs(); len(urls) > 0 {
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
	} else if dh, ok := dhs[desc.Digest]; ok {
		// No blob metadtata is stored in the content store. Try to get annotations from desc handlers.
		maps.Copy(desc.Annotations, filterAnnotationsForSave(dh.Annotations))
	}

	diffID := sr.getDiffID()
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
	if _, err := sr.cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = sr.compressionVariantsLeaseID()
		// do not make it flat lease to allow linking blobs using gc label
		return nil
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return err
	}
	if err := sr.cm.LeaseManager.AddResource(ctx, leases.Lease{ID: sr.compressionVariantsLeaseID()}, leases.Resource{
		ID:   desc.Digest.String(),
		Type: "content",
	}); err != nil {
		return err
	}
	cs := sr.cm.ContentStore
	blobDigest := sr.getBlob()
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
	sr.queueSize(sizeUnknown)
	if err := sr.commitMetadata(); err != nil {
		sr.mu.Unlock()
		return err
	}
	sr.mu.Unlock()
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
	if _, err := sr.cm.ContentStore.Info(ctx, sr.getBlob()); err != nil {
		return ocispecs.Descriptor{}, err
	}
	desc, err := sr.ociDesc(ctx, nil, true)
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

func (sr *immutableRef) Mount(ctx context.Context, readonly bool, s session.Group) (_ snapshot.Mountable, rerr error) {
	if sr.equalMutable != nil && !readonly {
		if err := sr.Finalize(ctx); err != nil {
			return nil, err
		}
	}

	if err := sr.Extract(ctx, s); err != nil {
		return nil, err
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.mountCache != nil {
		if readonly {
			return setReadonly(sr.mountCache), nil
		}
		return sr.mountCache, nil
	}

	var mnt snapshot.Mountable
	mnt, rerr = sr.mount(ctx)
	if rerr != nil {
		return nil, rerr
	}

	if readonly {
		mnt = setReadonly(mnt)
	}
	return mnt, nil
}

func (sr *immutableRef) ensureLocalContentBlob(ctx context.Context, s session.Group) error {
	if (sr.kind() == Layer || sr.kind() == BaseLayer) && !sr.getBlobOnly() {
		return nil
	}

	return sr.unlazy(ctx, sr.descHandlers, nil, s, true, true)
}

func (sr *immutableRef) Extract(ctx context.Context, s session.Group) (rerr error) {
	if (sr.kind() == Layer || sr.kind() == BaseLayer) && !sr.getBlobOnly() {
		return nil
	}

	return sr.unlazy(ctx, sr.descHandlers, nil, s, true, false)
}

func (sr *immutableRef) unlazy(ctx context.Context, dhs DescHandlers, pg progress.Controller, s session.Group, topLevel bool, ensureContentStore bool) error {
	_, err := g.Do(ctx, sr.ID()+"-unlazy", func(ctx context.Context) (_ *leaseutil.LeaseRef, rerr error) {
		if _, err := sr.cm.Snapshotter.Stat(ctx, sr.getSnapshotID()); err == nil {
			if !ensureContentStore {
				return nil, nil
			}
			if blob := sr.getBlob(); blob == "" {
				return nil, nil
			}
			if _, err := sr.cm.ContentStore.Info(ctx, sr.getBlob()); err == nil {
				return nil, nil
			}
		}

		switch sr.kind() {
		case Merge, Diff:
			return nil, sr.unlazyDiffMerge(ctx, dhs, pg, s, topLevel, ensureContentStore)
		case Layer, BaseLayer:
			return nil, sr.unlazyLayer(ctx, dhs, pg, s, ensureContentStore)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// should be called within sizeG.Do call for this ref's ID
func (sr *immutableRef) unlazyDiffMerge(ctx context.Context, dhs DescHandlers, pg progress.Controller, s session.Group, topLevel bool, ensureContentStore bool) (rerr error) {
	eg, egctx := errgroup.WithContext(ctx)
	var diffs []snapshot.Diff
	sr.layerWalk(func(sr *immutableRef) {
		var diff snapshot.Diff
		switch sr.kind() {
		case Diff:
			if sr.diffParents.lower != nil {
				diff.Lower = sr.diffParents.lower.getSnapshotID()
				eg.Go(func() error {
					return sr.diffParents.lower.unlazy(egctx, dhs, pg, s, false, ensureContentStore)
				})
			}
			if sr.diffParents.upper != nil {
				diff.Upper = sr.diffParents.upper.getSnapshotID()
				eg.Go(func() error {
					return sr.diffParents.upper.unlazy(egctx, dhs, pg, s, false, ensureContentStore)
				})
			}
		case Layer:
			diff.Lower = sr.layerParent.getSnapshotID()
			fallthrough
		case BaseLayer:
			diff.Upper = sr.getSnapshotID()
			eg.Go(func() error {
				return sr.unlazy(egctx, dhs, pg, s, false, ensureContentStore)
			})
		}
		diffs = append(diffs, diff)
	})
	if err := eg.Wait(); err != nil {
		return err
	}

	if pg != nil {
		action := "merging"
		if sr.kind() == Diff {
			action = "diffing"
		}
		progressID := sr.GetDescription()
		if topLevel {
			progressID = action
		}
		if progressID == "" {
			progressID = fmt.Sprintf("%s %s", action, sr.ID())
		}
		_, stopProgress := pg.Start(ctx)
		defer stopProgress(rerr)
		statusDone := pg.Status(progressID, action)
		defer statusDone()
	}

	return sr.cm.Snapshotter.Merge(ctx, sr.getSnapshotID(), diffs)
}

// should be called within sizeG.Do call for this ref's ID
func (sr *immutableRef) unlazyLayer(ctx context.Context, dhs DescHandlers, pg progress.Controller, s session.Group, ensureContentStore bool) (rerr error) {
	if !sr.getBlobOnly() {
		return nil
	}

	if sr.cm.Applier == nil {
		return errors.New("unlazy requires an applier")
	}

	if _, ok := leases.FromContext(ctx); !ok {
		leaseCtx, done, err := leaseutil.WithLease(ctx, sr.cm.LeaseManager, leaseutil.MakeTemporary)
		if err != nil {
			return err
		}
		defer done(leaseCtx)
		ctx = leaseCtx
	}

	if sr.GetLayerType() == "windows" {
		ctx = winlayers.UseWindowsLayerMode(ctx)
	}

	eg, egctx := errgroup.WithContext(ctx)

	parentID := ""
	if sr.layerParent != nil {
		eg.Go(func() error {
			if err := sr.layerParent.unlazy(egctx, dhs, pg, s, false, ensureContentStore); err != nil {
				return err
			}
			parentID = sr.layerParent.getSnapshotID()
			return nil
		})
	}

	desc, err := sr.ociDesc(ctx, dhs, true)
	if err != nil {
		return err
	}
	dh := dhs[desc.Digest]

	eg.Go(func() error {
		// unlazies if needed, otherwise a no-op
		return lazyRefProvider{
			ref:     sr,
			desc:    desc,
			dh:      dh,
			session: s,
		}.Unlazy(egctx)
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	if pg != nil {
		_, stopProgress := pg.Start(ctx)
		defer stopProgress(rerr)
		statusDone := pg.Status("extracting "+desc.Digest.String(), "extracting")
		defer statusDone()
	}

	key := fmt.Sprintf("extract-%s %s", identity.NewID(), sr.getChainID())

	err = sr.cm.Snapshotter.Prepare(ctx, key, parentID)
	if err != nil {
		return err
	}

	mountable, err := sr.cm.Snapshotter.Mounts(ctx, key)
	if err != nil {
		return err
	}
	mounts, unmount, err := mountable.Mount()
	if err != nil {
		return err
	}
	_, err = sr.cm.Applier.Apply(ctx, desc, mounts)
	if err != nil {
		unmount()
		return err
	}

	if err := unmount(); err != nil {
		return err
	}
	if err := sr.cm.Snapshotter.Commit(ctx, sr.getSnapshotID(), key); err != nil {
		if !errors.Is(err, cerrdefs.ErrAlreadyExists) {
			return err
		}
	}
	sr.queueBlobOnly(false)
	sr.queueSize(sizeUnknown)
	if err := sr.commitMetadata(); err != nil {
		return err
	}
	return nil
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
	if !sr.triggerLastUsed {
		return false
	}
	for r := range sr.refs {
		if r.shouldUpdateLastUsed() {
			return false
		}
	}
	return true
}

func (sr *immutableRef) release(ctx context.Context) (rerr error) {
	defer func() {
		l := bklog.G(ctx).WithFields(sr.traceLogFields())
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("released cache ref")
	}()

	delete(sr.refs, sr)
	if sr.updateLastUsedNow() {
		sr.updateLastUsed()
	}

	if len(sr.refs) == 0 {
		if err := sr.cm.LeaseManager.Delete(ctx, leases.Lease{ID: sr.viewLeaseID()}); err != nil && !cerrdefs.IsNotFound(err) {
			return err
		}
		sr.mountCache = nil
	}

	return nil
}

func (sr *immutableRef) Finalize(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.finalize(ctx)
}

// caller must hold cacheRecord.mu
func (cr *cacheRecord) finalize(ctx context.Context) error {
	mutable := cr.equalMutable
	if mutable == nil {
		return nil
	}

	_, err := cr.cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = cr.ID()
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, cerrdefs.ErrAlreadyExists) { // migrator adds leases for everything
			return errors.Wrap(err, "failed to create lease")
		}
	}

	if err := cr.cm.LeaseManager.AddResource(ctx, leases.Lease{ID: cr.ID()}, leases.Resource{
		ID:   cr.getSnapshotID(),
		Type: "snapshots/" + cr.cm.Snapshotter.Name(),
	}); err != nil {
		cr.cm.LeaseManager.Delete(context.TODO(), leases.Lease{ID: cr.ID()})
		return errors.Wrapf(err, "failed to add snapshot %s to lease", cr.getSnapshotID())
	}

	if err := cr.cm.Snapshotter.Commit(ctx, cr.getSnapshotID(), mutable.getSnapshotID()); err != nil {
		cr.cm.LeaseManager.Delete(context.TODO(), leases.Lease{ID: cr.ID()})
		return errors.Wrapf(err, "failed to commit %s to %s during finalize", mutable.getSnapshotID(), cr.getSnapshotID())
	}
	cr.mountCache = nil

	mutable.dead = true
	go func() {
		cr.cm.mu.Lock()
		defer cr.cm.mu.Unlock()
		if err := mutable.remove(context.TODO(), true); err != nil {
			bklog.G(ctx).Error(err)
		}
	}()

	cr.equalMutable = nil
	cr.clearEqualMutable()
	return cr.commitMetadata()
}

func (sr *mutableRef) shouldUpdateLastUsed() bool {
	return sr.triggerLastUsed
}

func (sr *mutableRef) commit() (_ *immutableRef, rerr error) {
	if !sr.mutable || len(sr.refs) == 0 {
		return nil, errors.Wrapf(errInvalid, "invalid mutable ref %p", sr)
	}

	id := identity.NewID()
	md := sr.cm.ensureMetadata(id)
	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		cm:            sr.cm,
		parentRefs:    sr.parentRefs.clone(),
		refs:          make(map[ref]struct{}),
		cacheMetadata: md,
	}

	l, err := sr.cm.LeaseManager.Create(context.TODO(), func(l *leases.Lease) error {
		l.ID = id
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, cerrdefs.ErrAlreadyExists) {
			return nil, errors.Wrap(err, "failed to create lease")
		}
		l.ID = id
	}
	defer func() {
		if rerr != nil {
			ctx := context.WithoutCancel(context.TODO())
			if err := sr.cm.LeaseManager.Delete(ctx, leases.Lease{ID: l.ID}); err != nil && !cerrdefs.IsNotFound(err) {
				bklog.G(ctx).WithError(err).Warn("failed to clean up immutable lease after commit failure")
			}
		}
	}()

	if err := sr.cm.LeaseManager.AddResource(context.TODO(), leases.Lease{ID: id}, leases.Resource{
		ID:   id,
		Type: "snapshots/" + sr.cm.Snapshotter.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return nil, errors.Wrapf(err, "failed to add snapshot %s to lease", id)
	}

	if err := sr.cm.Snapshotter.Commit(context.TODO(), id, sr.getSnapshotID()); err != nil {
		return nil, errors.Wrapf(err, "failed to commit %s to immutable %s", sr.getSnapshotID(), id)
	}

	if descr := sr.GetDescription(); descr != "" {
		if err := md.queueDescription(descr); err != nil {
			return nil, err
		}
	}

	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs); err != nil {
		return nil, err
	}

	sr.cm.records[id] = rec

	if err := sr.commitMetadata(); err != nil {
		return nil, err
	}

	md.queueCommitted(true)
	md.queueSize(sizeUnknown)
	md.queueSnapshotID(id)
	if err := md.commitMetadata(); err != nil {
		return nil, err
	}

	ref := rec.ref(true, sr.descHandlers)
	return ref, nil
}

func (sr *mutableRef) Mount(ctx context.Context, readonly bool, s session.Group) (_ snapshot.Mountable, rerr error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.mutableMount(ctx, readonly, s)
}

// caller must hold cacheRecord.mu
func (cr *cacheRecord) mutableMount(ctx context.Context, readonly bool, s session.Group) (_ snapshot.Mountable, rerr error) {
	if cr.mountCache != nil {
		if readonly {
			return setReadonly(cr.mountCache), nil
		}
		return cr.mountCache, nil
	}

	var mnt snapshot.Mountable
	mnt, rerr = cr.mount(ctx)
	if rerr != nil {
		return nil, rerr
	}

	if cr.GetRecordType() == client.UsageRecordTypeCacheMount {
		// Make the mounts sharable. We don't do this for immutableRef mounts because
		// it requires the raw []mount.Mount for computing diff on overlayfs.
		mnt = cr.cm.mountPool.setSharable(mnt)
	}

	cr.mountCache = mnt
	if readonly {
		mnt = setReadonly(mnt)
	}
	return mnt, nil
}

func (sr *mutableRef) Commit(ctx context.Context) (ImmutableRef, error) {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	return sr.commit()
}

func (sr *mutableRef) Release(ctx context.Context) error {
	sr.cm.mu.Lock()
	defer sr.cm.mu.Unlock()

	sr.mu.Lock()
	defer sr.mu.Unlock()

	return sr.release(ctx)
}

func (sr *mutableRef) release(ctx context.Context) (rerr error) {
	defer func() {
		l := bklog.G(ctx).WithFields(sr.traceLogFields())
		if rerr != nil {
			l = l.WithError(rerr)
		}
		l.Trace("released cache ref")
	}()
	delete(sr.refs, sr)

	if !sr.HasCachePolicyRetain() {
		return sr.remove(ctx, true)
	}
	if sr.shouldUpdateLastUsed() {
		sr.updateLastUsed()
		sr.triggerLastUsed = false
	}
	return nil
}

func setReadonly(mounts snapshot.Mountable) snapshot.Mountable {
	return &readOnlyMounter{mounts}
}

type readOnlyMounter struct {
	snapshot.Mountable
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
