package snapshots

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/labels"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/docker/docker/pkg/idtools"
	digest "github.com/opencontainers/go-digest"
	imagespecidentity "github.com/opencontainers/image-spec/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var (
	ErrLocked   = errors.New("locked")
	errNotFound = errors.New("not found")
	errInvalid  = errors.New("invalid")
)

type SnapshotManagerOpt struct {
	Snapshotter   snapshot.Snapshotter
	ContentStore  content.Store
	LeaseManager  leases.Manager
	Applier       diff.Applier
	Differ        diff.Comparer
	MountPoolRoot string
}

type Accessor interface {
	MetadataStore

	GetByBlob(ctx context.Context, desc ocispecs.Descriptor, parent ImmutableRef, opts ...RefOption) (ImmutableRef, error)
	Get(ctx context.Context, id string, opts ...RefOption) (ImmutableRef, error)
	GetBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (ImmutableRef, error)

	New(ctx context.Context, parent ImmutableRef, s session.Group, opts ...RefOption) (MutableRef, error)
	GetMutable(ctx context.Context, id string, opts ...RefOption) (MutableRef, error) // Rebase?
	GetMutableBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (MutableRef, error)
	IdentityMapping() *idtools.IdentityMapping
	// TODO: keep Merge/Diff only while core/directory and core/changeset still call them directly.
	// Once those callers are moved, remove these from snapshots entirely.
	Merge(ctx context.Context, parents []ImmutableRef, opts ...RefOption) (ImmutableRef, error)
	Diff(ctx context.Context, lower, upper ImmutableRef, opts ...RefOption) (ImmutableRef, error)
}

type SnapshotManager interface {
	Accessor
	Close() error
}

type snapshotManager struct {
	records       map[string]*cacheRecord
	mu            sync.Mutex
	Snapshotter   snapshot.MergeSnapshotter
	ContentStore  content.Store
	LeaseManager  leases.Manager
	Applier       diff.Applier
	Differ        diff.Comparer
	metadataStore *metadataStore

	mountPool sharableMountPool

	unlazyG flightcontrol.Group[struct{}]
}

func NewSnapshotManager(opt SnapshotManagerOpt) (SnapshotManager, error) {
	cm := &snapshotManager{
		Snapshotter:   snapshot.NewMergeSnapshotter(context.TODO(), opt.Snapshotter, opt.LeaseManager),
		ContentStore:  opt.ContentStore,
		LeaseManager:  opt.LeaseManager,
		Applier:       opt.Applier,
		Differ:        opt.Differ,
		metadataStore: newMetadataStore(),
		records:       make(map[string]*cacheRecord),
	}

	p, err := newSharableMountPool(opt.MountPoolRoot)
	if err != nil {
		return nil, err
	}
	cm.mountPool = p

	if err := cm.init(context.TODO()); err != nil {
		return nil, err
	}

	// cm.scheduleGC(5 * time.Minute)

	return cm, nil
}

func (cm *snapshotManager) GetByBlob(ctx context.Context, desc ocispecs.Descriptor, parent ImmutableRef, opts ...RefOption) (ir ImmutableRef, rerr error) {
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return nil, err
	}
	chainID := diffID
	blobChainID := imagespecidentity.ChainID([]digest.Digest{desc.Digest, diffID})

	descHandlers := descHandlersOf(opts...)
	if desc.Digest != "" {
		if _, err := cm.ContentStore.Info(ctx, desc.Digest); err != nil && !errors.Is(err, cerrdefs.ErrNotFound) {
			return nil, err
		}
	}

	var p *immutableRef
	if parent != nil {
		p2, err := cm.Get(ctx, parent.ID(), NoUpdateLastUsed, descHandlers)
		if err != nil {
			return nil, err
		}
		p = p2.(*immutableRef)

		if err := p.Finalize(ctx); err != nil {
			p.Release(context.TODO())
			return nil, err
		}

		if p.getChainID() == "" || p.getBlobChainID() == "" {
			p.Release(context.TODO())
			return nil, errors.Errorf("failed to get ref by blob on non-addressable parent")
		}
		chainID = imagespecidentity.ChainID([]digest.Digest{p.getChainID(), chainID})
		blobChainID = imagespecidentity.ChainID([]digest.Digest{p.getBlobChainID(), blobChainID})
	}

	releaseParent := false
	defer func() {
		if releaseParent || rerr != nil && p != nil {
			p.Release(context.WithoutCancel(ctx))
		}
	}()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	sis, err := cm.searchBlobchain(ctx, blobChainID)
	if err != nil {
		return nil, err
	}

	for _, si := range sis {
		ref, err := cm.get(ctx, si.ID(), opts...)
		if err != nil && !IsNotFound(err) {
			return nil, errors.Wrapf(err, "failed to get record %s by blobchainid", sis[0].ID())
		}
		if ref == nil {
			continue
		}
		if p != nil {
			releaseParent = true
		}
		if err := setImageRefMetadata(ref.cacheMetadata, opts...); err != nil {
			return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", ref.ID())
		}
		return ref, nil
	}

	sis, err = cm.searchChain(ctx, chainID)
	if err != nil {
		return nil, err
	}

	var link *immutableRef
	for _, si := range sis {
		ref, err := cm.get(ctx, si.ID(), opts...)
		if err != nil && !IsNotFound(err) {
			return nil, errors.Wrapf(err, "failed to get record %s by chainid", si.ID())
		}
		if ref != nil {
			link = ref
			break
		}
	}

	id := identity.NewID()
	snapshotID := chainID.String()
	if link != nil {
		snapshotID = link.getSnapshotID()
		go link.Release(context.TODO())
	}

	l, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = id
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create lease")
	}

	defer func() {
		if rerr != nil {
			ctx := context.WithoutCancel(ctx)
			if err := cm.LeaseManager.Delete(ctx, leases.Lease{
				ID: l.ID,
			}); err != nil {
				bklog.G(ctx).Errorf("failed to remove lease: %+v", err)
			}
		}
	}()

	if err := cm.LeaseManager.AddResource(ctx, l, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return nil, errors.Wrapf(err, "failed to add snapshot %s to lease", id)
	}

	if desc.Digest != "" {
		if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: id}, leases.Resource{
			ID:   desc.Digest.String(),
			Type: "content",
		}); err != nil {
			return nil, errors.Wrapf(err, "failed to add blob %s to lease", id)
		}
	}

	md := cm.ensureMetadata(id)

	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		cm:            cm,
		refs:          make(map[ref]struct{}),
		parentRefs:    parentRefs{layerParent: p},
		cacheMetadata: md,
	}

	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs, opts...); err != nil {
		return nil, err
	}

	if err := setImageRefMetadata(rec.cacheMetadata, opts...); err != nil {
		return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", rec.ID())
	}

	rec.queueDiffID(diffID)
	rec.queueBlob(desc.Digest)
	rec.queueChainID(chainID)
	rec.queueBlobChainID(blobChainID)
	rec.queueSnapshotID(snapshotID)
	rec.queueBlobOnly(true)
	rec.queueMediaType(desc.MediaType)
	rec.queueBlobSize(desc.Size)
	rec.appendURLs(desc.URLs)
	rec.queueCommitted(true)

	if err := rec.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec

	ref := rec.ref(true, descHandlers)
	return ref, nil
}

// init loads all snapshots from metadata state and tries to load the records
// from the snapshotter. If snaphot can't be found, metadata is deleted as well.
func (cm *snapshotManager) init(ctx context.Context) error {
	_ = ctx
	// Snapshot metadata is in-memory only now; there is no persisted metadata DB to hydrate.
	return nil
}

// IdentityMapping returns the userns remapping used for refs
func (cm *snapshotManager) IdentityMapping() *idtools.IdentityMapping {
	return cm.Snapshotter.IdentityMapping()
}

// Close closes the manager and releases the metadata database lock. No other
// method should be called after Close.
func (cm *snapshotManager) Close() error {
	return cm.metadataStore.close()
}

// Get returns an immutable snapshot reference for ID
func (cm *snapshotManager) Get(ctx context.Context, id string, opts ...RefOption) (ImmutableRef, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.get(ctx, id, opts...)
}

func (cm *snapshotManager) GetBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (ImmutableRef, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if err := cm.rehydrateSnapshotMetadataLocked(ctx, snapshotID, true); err != nil {
		return nil, err
	}
	return cm.get(ctx, snapshotID, opts...)
}

// get requires manager lock to be taken
func (cm *snapshotManager) get(ctx context.Context, id string, opts ...RefOption) (*immutableRef, error) {
	rec, err := cm.getRecord(ctx, id, opts...)
	if err != nil {
		return nil, err
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()

	triggerUpdate := true
	for _, o := range opts {
		if o == NoUpdateLastUsed {
			triggerUpdate = false
		}
	}

	descHandlers := descHandlersOf(opts...)

	if rec.mutable {
		if len(rec.refs) != 0 {
			return nil, errors.Wrapf(ErrLocked, "%s is locked", id)
		}
		return rec.mref(triggerUpdate, descHandlers).commit()
	}

	return rec.ref(triggerUpdate, descHandlers), nil
}

// getRecord returns record for id. Requires manager lock.
func (cm *snapshotManager) getRecord(ctx context.Context, id string, opts ...RefOption) (cr *cacheRecord, retErr error) {
	if rec, ok := cm.records[id]; ok {
		if rec.isDead() {
			return nil, errors.Wrapf(errNotFound, "failed to get dead record %s", id)
		}
		return rec, nil
	}

	md, ok := cm.getMetadata(id)
	if !ok {
		return nil, errors.Wrap(errNotFound, id)
	}

	parents, err := cm.parentsOf(ctx, md, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get parents")
	}
	defer func() {
		if retErr != nil {
			parents.release(context.WithoutCancel(ctx))
		}
	}()

	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		mutable:       !md.getCommitted(),
		cm:            cm,
		refs:          make(map[ref]struct{}),
		parentRefs:    parents,
		cacheMetadata: md,
	}

	// TODO:(sipsma) this is kludge to deal with a bug in v0.10.{0,1} where
	// merge and diff refs didn't have committed set to true:
	// https://github.com/dagger/dagger/internal/buildkit/issues/2740
	if kind := rec.kind(); kind == Merge || kind == Diff {
		rec.mutable = false
	}

	// the record was deleted but we crashed before data on disk was removed
	if md.getDeleted() {
		if err := rec.remove(ctx, true); err != nil {
			return nil, err
		}
		return nil, errors.Wrapf(errNotFound, "failed to get deleted record %s", id)
	}

	if rec.mutable {
		// If the record is mutable, then the snapshot must exist
		if _, err := cm.Snapshotter.Stat(ctx, rec.ID()); err != nil {
			if !cerrdefs.IsNotFound(err) {
				return nil, errors.Wrap(err, "failed to check mutable ref snapshot")
			}
			// the snapshot doesn't exist, clear this record
			if err := rec.remove(ctx, true); err != nil {
				return nil, errors.Wrap(err, "failed to remove mutable rec with missing snapshot")
			}
			return nil, errors.Wrap(errNotFound, rec.ID())
		}
		// check if the engine had a hard crash and left an overlay volatile dir over, in which
		// case we should just remove the cache record
		dirtyVolatile, err := rec.hasDirtyVolatile(ctx)
		if err != nil {
			return nil, err
		}
		if dirtyVolatile {
			if err := rec.remove(ctx, true); err != nil {
				return nil, err
			}
			return nil, errors.Wrapf(errNotFound, "failed to get record %s with dirty volatile overlay", id)
		}
	}

	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs, opts...); err != nil {
		return nil, err
	}

	if err := setImageRefMetadata(rec.cacheMetadata, opts...); err != nil {
		return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", rec.ID())
	}

	cm.records[id] = rec
	return rec, nil
}

func (cm *snapshotManager) parentsOf(ctx context.Context, md *cacheMetadata, opts ...RefOption) (ps parentRefs, rerr error) {
	defer func() {
		if rerr != nil {
			ps.release(context.WithoutCancel(ctx))
		}
	}()
	if parentID := md.getParent(); parentID != "" {
		p, err := cm.get(ctx, parentID, append(opts, NoUpdateLastUsed))
		if err != nil {
			return ps, err
		}
		ps.layerParent = p
		return ps, nil
	}
	for _, parentID := range md.getMergeParents() {
		p, err := cm.get(ctx, parentID, append(opts, NoUpdateLastUsed))
		if err != nil {
			return ps, err
		}
		ps.mergeParents = append(ps.mergeParents, p)
	}
	if lowerParentID := md.getLowerDiffParent(); lowerParentID != "" {
		p, err := cm.get(ctx, lowerParentID, append(opts, NoUpdateLastUsed))
		if err != nil {
			return ps, err
		}
		if ps.diffParents == nil {
			ps.diffParents = &diffParents{}
		}
		ps.diffParents.lower = p
	}
	if upperParentID := md.getUpperDiffParent(); upperParentID != "" {
		p, err := cm.get(ctx, upperParentID, append(opts, NoUpdateLastUsed))
		if err != nil {
			return ps, err
		}
		if ps.diffParents == nil {
			ps.diffParents = &diffParents{}
		}
		ps.diffParents.upper = p
	}
	return ps, nil
}

func (cm *snapshotManager) New(ctx context.Context, s ImmutableRef, sess session.Group, opts ...RefOption) (mr MutableRef, err error) {
	id := identity.NewID()

	var parent *immutableRef
	var parentSnapshotID string
	if s != nil {
		if _, ok := s.(*immutableRef); ok {
			parent = s.Clone().(*immutableRef)
		} else {
			p, err := cm.Get(ctx, s.ID(), append(opts, NoUpdateLastUsed)...)
			if err != nil {
				return nil, err
			}
			parent = p.(*immutableRef)
		}
		if err := parent.Finalize(ctx); err != nil {
			return nil, err
		}
		if err := parent.Extract(ctx, sess); err != nil {
			return nil, err
		}
		parentSnapshotID = parent.getSnapshotID()
	}

	defer func() {
		if err != nil && parent != nil {
			parent.Release(context.WithoutCancel(ctx))
		}
	}()

	l, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = id
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create lease")
	}

	defer func() {
		if err != nil {
			ctx := context.WithoutCancel(ctx)
			if err := cm.LeaseManager.Delete(ctx, leases.Lease{
				ID: l.ID,
			}); err != nil {
				bklog.G(ctx).Errorf("failed to remove lease: %+v", err)
			}
		}
	}()

	snapshotID := id
	if err := cm.LeaseManager.AddResource(ctx, l, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return nil, errors.Wrapf(err, "failed to add snapshot %s to lease", snapshotID)
	}

	err = cm.Snapshotter.Prepare(ctx, snapshotID, parentSnapshotID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to prepare %v as %s", parentSnapshotID, snapshotID)
	}

	// parent refs are possibly lazy so keep it hold the description handlers.
	var dhs DescHandlers
	if parent != nil {
		dhs = parent.descHandlers
	}

	// All metadataStore and records map mutations must happen under cm.mu.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)

	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		mutable:       true,
		cm:            cm,
		refs:          make(map[ref]struct{}),
		parentRefs:    parentRefs{layerParent: parent},
		cacheMetadata: md,
	}

	opts = append(opts, withSnapshotID(snapshotID))
	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs, opts...); err != nil {
		return nil, err
	}

	if err := setImageRefMetadata(rec.cacheMetadata, opts...); err != nil {
		return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", rec.ID())
	}

	cm.records[id] = rec // TODO: save to db
	return rec.mref(true, dhs), nil
}

func (cm *snapshotManager) GetMutable(ctx context.Context, id string, opts ...RefOption) (MutableRef, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	rec, err := cm.getRecord(ctx, id, opts...)
	if err != nil {
		return nil, err
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.mutable {
		return nil, errors.Wrapf(errInvalid, "%s is not mutable", id)
	}

	if len(rec.refs) != 0 {
		return nil, errors.Wrapf(ErrLocked, "%s is locked", id)
	}

	return rec.mref(true, descHandlersOf(opts...)), nil
}

func (cm *snapshotManager) GetMutableBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (MutableRef, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if err := cm.rehydrateSnapshotMetadataLocked(ctx, snapshotID, false); err != nil {
		return nil, err
	}
	rec, err := cm.getRecord(ctx, snapshotID, opts...)
	if err != nil {
		return nil, err
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.mutable {
		return nil, errors.Wrapf(errInvalid, "%s is not mutable", snapshotID)
	}
	if len(rec.refs) != 0 {
		return nil, errors.Wrapf(ErrLocked, "%s is locked", snapshotID)
	}
	return rec.mref(true, descHandlersOf(opts...)), nil
}

func (cm *snapshotManager) rehydrateSnapshotMetadataLocked(ctx context.Context, snapshotID string, committed bool) error {
	if snapshotID == "" {
		return errors.New("empty snapshot ID")
	}
	if _, ok := cm.records[snapshotID]; ok {
		return nil
	}
	if _, ok := cm.getMetadata(snapshotID); ok {
		return nil
	}
	if _, err := cm.Snapshotter.Stat(ctx, snapshotID); err != nil {
		if cerrdefs.IsNotFound(err) {
			return errors.Wrap(errNotFound, snapshotID)
		}
		return errors.Wrapf(err, "stat snapshot %s", snapshotID)
	}
	if _, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = snapshotID
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return errors.Wrapf(err, "create lease for rehydrated snapshot %s", snapshotID)
	}
	if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: snapshotID}, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	}); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return errors.Wrapf(err, "attach rehydrated snapshot %s to lease", snapshotID)
	}

	md := cm.ensureMetadata(snapshotID)
	if err := md.queueSnapshotID(snapshotID); err != nil {
		return err
	}
	if err := md.queueCommitted(committed); err != nil {
		return err
	}
	if err := md.SetCachePolicyRetain(); err != nil {
		return err
	}
	if err := md.queueDescription("rehydrated persisted snapshot"); err != nil {
		return err
	}
	if err := md.commitMetadata(); err != nil {
		return err
	}
	return nil
}

func (cm *snapshotManager) Merge(ctx context.Context, inputParents []ImmutableRef, opts ...RefOption) (ir ImmutableRef, rerr error) {
	// TODO:(sipsma) optimize merge further by
	// * Removing repeated occurrences of input layers (only leaving the uppermost)
	// * Reusing existing merges that are equivalent to this one
	// * Reusing existing merges that can be used as a base for this one
	// * Calculating diffs only once (across both merges and during computeBlobChain). Save diff metadata so it can be reapplied.
	// These optimizations may make sense here in cache, in the snapshotter or both.
	// Be sure that any optimizations handle existing pre-optimization refs correctly.

	parents := parentRefs{mergeParents: make([]*immutableRef, 0, len(inputParents))}
	dhs := make(map[digest.Digest]*DescHandler)
	defer func() {
		if rerr != nil {
			parents.release(context.WithoutCancel(ctx))
		}
	}()
	for _, inputParent := range inputParents {
		if inputParent == nil {
			continue
		}
		var parent *immutableRef
		if p, ok := inputParent.(*immutableRef); ok {
			parent = p
		} else {
			// inputParent implements ImmutableRef but isn't our internal struct, get an instance of the internal struct
			// by calling Get on its ID.
			p, err := cm.Get(ctx, inputParent.ID(), append(opts, NoUpdateLastUsed)...)
			if err != nil {
				return nil, err
			}
			parent = p.(*immutableRef)
			defer parent.Release(context.TODO())
		}
		// On success, cloned parents will be not be released and will be owned by the returned ref
		switch parent.kind() {
		case Merge:
			// if parent is itself a merge, flatten it out by just setting our parents directly to its parents
			for _, grandparent := range parent.mergeParents {
				parents.mergeParents = append(parents.mergeParents, grandparent.clone())
			}
		default:
			parents.mergeParents = append(parents.mergeParents, parent.clone())
		}
		for dgst, handler := range parent.descHandlers {
			dhs[dgst] = handler
		}
	}

	// On success, createMergeRef takes ownership of parents
	mergeRef, err := cm.createMergeRef(ctx, parents, dhs, opts...)
	if err != nil {
		return nil, err
	}
	return mergeRef, nil
}

func (cm *snapshotManager) createMergeRef(ctx context.Context, parents parentRefs, dhs DescHandlers, opts ...RefOption) (ir *immutableRef, rerr error) {
	if len(parents.mergeParents) == 0 {
		// merge of nothing is nothing
		return nil, nil
	}
	if len(parents.mergeParents) == 1 {
		// merge of 1 thing is that thing
		return parents.mergeParents[0], nil
	}

	for _, parent := range parents.mergeParents {
		if err := parent.Finalize(ctx); err != nil {
			return nil, errors.Wrapf(err, "failed to finalize parent during merge")
		}
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Build the new ref
	id := identity.NewID()
	md := cm.ensureMetadata(id)

	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		mutable:       false,
		cm:            cm,
		cacheMetadata: md,
		parentRefs:    parents,
		refs:          make(map[ref]struct{}),
	}

	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs, opts...); err != nil {
		return nil, err
	}

	snapshotID := id
	l, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = id
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create lease")
	}
	defer func() {
		if rerr != nil {
			ctx := context.WithoutCancel(ctx)
			if err := cm.LeaseManager.Delete(ctx, leases.Lease{
				ID: l.ID,
			}); err != nil {
				bklog.G(ctx).Errorf("failed to remove lease: %+v", err)
			}
		}
	}()

	if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: id}, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	}); err != nil {
		return nil, err
	}

	rec.queueSnapshotID(snapshotID)
	rec.queueCommitted(true)
	if err := rec.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec

	return rec.ref(true, dhs), nil
}

func (cm *snapshotManager) Diff(ctx context.Context, lower, upper ImmutableRef, opts ...RefOption) (ir ImmutableRef, rerr error) {
	if lower == nil {
		return nil, errors.New("lower ref for diff cannot be nil")
	}

	var dps diffParents
	parents := parentRefs{diffParents: &dps}
	dhs := make(map[digest.Digest]*DescHandler)
	defer func() {
		if rerr != nil {
			parents.release(context.WithoutCancel(ctx))
		}
	}()
	for i, inputParent := range []ImmutableRef{lower, upper} {
		if inputParent == nil {
			continue
		}
		var parent *immutableRef
		if p, ok := inputParent.(*immutableRef); ok {
			parent = p
		} else {
			// inputParent implements ImmutableRef but isn't our internal struct, get an instance of the internal struct
			// by calling Get on its ID.
			p, err := cm.Get(ctx, inputParent.ID(), append(opts, NoUpdateLastUsed)...)
			if err != nil {
				return nil, err
			}
			parent = p.(*immutableRef)
			defer parent.Release(context.TODO())
		}
		// On success, cloned parents will not be released and will be owned by the returned ref
		if i == 0 {
			dps.lower = parent.clone()
		} else {
			dps.upper = parent.clone()
		}
		for dgst, handler := range parent.descHandlers {
			dhs[dgst] = handler
		}
	}

	// Check to see if lower is an ancestor of upper. If so, define the diff as a merge
	// of the layers separating the two. This can result in a different diff than just
	// running the differ directly on lower and upper, but this is chosen as a default
	// behavior in order to maximize layer re-use in the default case. We may add an
	// option for controlling this behavior in the future if it's needed.
	if dps.upper != nil {
		lowerLayers := dps.lower.layerChain()
		upperLayers := dps.upper.layerChain()
		var lowerIsAncestor bool
		// when upper is only 1 layer different than lower, we can skip this as we
		// won't need a merge in order to get optimal behavior.
		if len(upperLayers) > len(lowerLayers)+1 {
			lowerIsAncestor = true
			for i, lowerLayer := range lowerLayers {
				if lowerLayer.ID() != upperLayers[i].ID() {
					lowerIsAncestor = false
					break
				}
			}
		}
		if lowerIsAncestor {
			mergeParents := parentRefs{mergeParents: make([]*immutableRef, len(upperLayers)-len(lowerLayers))}
			defer func() {
				if rerr != nil {
					mergeParents.release(context.WithoutCancel(ctx))
				}
			}()
			for i := len(lowerLayers); i < len(upperLayers); i++ {
				subUpper := upperLayers[i]
				subLower := subUpper.layerParent
				// On success, cloned refs will not be released and will be owned by the returned ref
				if subLower == nil {
					mergeParents.mergeParents[i-len(lowerLayers)] = subUpper.clone()
				} else {
					subParents := parentRefs{diffParents: &diffParents{lower: subLower.clone(), upper: subUpper.clone()}}
					diffRef, err := cm.createDiffRef(ctx, subParents, subUpper.descHandlers,
						WithDescription(fmt.Sprintf("diff %q -> %q", subLower.ID(), subUpper.ID())))
					if err != nil {
						subParents.release(context.TODO())
						return nil, err
					}
					mergeParents.mergeParents[i-len(lowerLayers)] = diffRef
				}
			}
			// On success, createMergeRef takes ownership of mergeParents
			mergeRef, err := cm.createMergeRef(ctx, mergeParents, dhs)
			if err != nil {
				return nil, err
			}
			parents.release(context.TODO())
			return mergeRef, nil
		}
	}

	// On success, createDiffRef takes ownership of parents
	diffRef, err := cm.createDiffRef(ctx, parents, dhs, opts...)
	if err != nil {
		return nil, err
	}
	return diffRef, nil
}

func (cm *snapshotManager) createDiffRef(ctx context.Context, parents parentRefs, dhs DescHandlers, opts ...RefOption) (ir *immutableRef, rerr error) {
	dps := parents.diffParents
	if err := dps.lower.Finalize(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to finalize lower parent during diff")
	}
	if dps.upper != nil {
		if err := dps.upper.Finalize(ctx); err != nil {
			return nil, errors.Wrapf(err, "failed to finalize upper parent during diff")
		}
	}

	id := identity.NewID()

	snapshotID := id

	l, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = id
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create lease")
	}
	defer func() {
		if rerr != nil {
			ctx := context.WithoutCancel(ctx)
			if err := cm.LeaseManager.Delete(ctx, leases.Lease{
				ID: l.ID,
			}); err != nil {
				bklog.G(ctx).Errorf("failed to remove lease: %+v", err)
			}
		}
	}()

	if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: id}, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	}); err != nil {
		return nil, err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Build the new ref
	md := cm.ensureMetadata(id)

	rec := &cacheRecord{
		mu:            &sync.Mutex{},
		mutable:       false,
		cm:            cm,
		cacheMetadata: md,
		parentRefs:    parents,
		refs:          make(map[ref]struct{}),
	}

	if err := initializeMetadata(rec.cacheMetadata, rec.parentRefs, opts...); err != nil {
		return nil, err
	}

	rec.queueSnapshotID(snapshotID)
	rec.queueCommitted(true)
	if err := rec.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec

	return rec.ref(true, dhs), nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}

type RefOption interface{}

type cachePolicy int

const (
	cachePolicyDefault cachePolicy = iota
	cachePolicyRetain
)

type noUpdateLastUsed struct{}

var NoUpdateLastUsed noUpdateLastUsed

func CachePolicyRetain(m *cacheMetadata) error {
	return m.SetCachePolicyRetain()
}

func CachePolicyDefault(m *cacheMetadata) error {
	return m.SetCachePolicyDefault()
}

func WithDescription(descr string) RefOption {
	return func(m *cacheMetadata) error {
		return m.queueDescription(descr)
	}
}

func WithRecordType(t client.UsageRecordType) RefOption {
	return func(m *cacheMetadata) error {
		return m.queueRecordType(t)
	}
}

func WithCreationTime(tm time.Time) RefOption {
	return func(m *cacheMetadata) error {
		return m.queueCreatedAt(tm)
	}
}

// Need a separate type for imageRef because it needs to be called outside
// initializeMetadata while still being a RefOption, so wrapping it in a
// different type ensures initializeMetadata won't catch it too and duplicate
// setting the metadata.
type imageRefOption func(m *cacheMetadata) error

// WithImageRef appends the given imageRef to the cache ref's metadata
func WithImageRef(imageRef string) RefOption {
	return imageRefOption(func(m *cacheMetadata) error {
		return m.appendImageRef(imageRef)
	})
}

func setImageRefMetadata(m *cacheMetadata, opts ...RefOption) error {
	for _, opt := range opts {
		if fn, ok := opt.(imageRefOption); ok {
			if err := fn(m); err != nil {
				return err
			}
		}
	}
	return m.commitMetadata()
}

func withSnapshotID(id string) RefOption {
	return imageRefOption(func(m *cacheMetadata) error {
		return m.queueSnapshotID(id)
	})
}

func initializeMetadata(m *cacheMetadata, parents parentRefs, opts ...RefOption) error {
	if tm := m.GetCreatedAt(); !tm.IsZero() {
		return nil
	}

	switch {
	case parents.layerParent != nil:
		if err := m.queueParent(parents.layerParent.ID()); err != nil {
			return err
		}
	case len(parents.mergeParents) > 0:
		var ids []string
		for _, p := range parents.mergeParents {
			ids = append(ids, p.ID())
		}
		if err := m.queueMergeParents(ids); err != nil {
			return err
		}
	case parents.diffParents != nil:
		if parents.diffParents.lower != nil {
			if err := m.queueLowerDiffParent(parents.diffParents.lower.ID()); err != nil {
				return err
			}
		}
		if parents.diffParents.upper != nil {
			if err := m.queueUpperDiffParent(parents.diffParents.upper.ID()); err != nil {
				return err
			}
		}
	}

	if err := m.queueCreatedAt(time.Now()); err != nil {
		return err
	}

	for _, opt := range opts {
		if fn, ok := opt.(func(*cacheMetadata) error); ok {
			if err := fn(m); err != nil {
				return err
			}
		}
	}

	return m.commitMetadata()
}

func diffIDFromDescriptor(desc ocispecs.Descriptor) (digest.Digest, error) {
	diffIDStr, ok := desc.Annotations[labels.LabelUncompressed]
	if !ok {
		return "", errors.Errorf("missing uncompressed annotation for %s", desc.Digest)
	}
	diffID, err := digest.Parse(diffIDStr)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse diffID %q for %s", diffIDStr, desc.Digest)
	}
	return diffID, nil
}
