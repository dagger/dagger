package snapshots

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/labels"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/snapshots/fsdiff"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/moby/locker"
	digest "github.com/opencontainers/go-digest"
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

type ImportedImage struct {
	Ref          string
	ManifestDesc ocispecs.Descriptor
	ConfigDesc   ocispecs.Descriptor
	Layers       []ocispecs.Descriptor
	Nonlayers    []ocispecs.Descriptor
}

type ImportImageOpts struct {
	ImageRef   string
	RecordType client.UsageRecordType
}

type Accessor interface {
	MetadataStore

	Get(ctx context.Context, id string, opts ...RefOption) (ImmutableRef, error)
	GetBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (ImmutableRef, error)

	New(ctx context.Context, parent ImmutableRef, opts ...RefOption) (MutableRef, error)
	GetMutable(ctx context.Context, id string, opts ...RefOption) (MutableRef, error) // Rebase?
	GetMutableBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (MutableRef, error)
	ImportImage(ctx context.Context, img *ImportedImage, opts ImportImageOpts) (ImmutableRef, error)
	ApplySnapshotDiff(ctx context.Context, lower, upper ImmutableRef, opts ...RefOption) (ImmutableRef, error)
	Merge(ctx context.Context, parents []ImmutableRef, opts ...RefOption) (ImmutableRef, error)
}

type SnapshotManager interface {
	Accessor
	AttachLease(ctx context.Context, leaseID, snapshotID string) error
	RemoveLease(ctx context.Context, leaseID string) error
	LoadPersistentMetadata(rows PersistentMetadataRows) error
	PersistentMetadataRows() PersistentMetadataRows
	DeleteStaleDaggerOwnerLeases(ctx context.Context, keep map[string]struct{}) error
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

	snapshotContentDigests map[string]map[digest.Digest]struct{}
	importedLayerByBlob    map[ImportedLayerBlobKey]string
	importedLayerByDiff    map[ImportedLayerDiffKey]string
	snapshotOwnerLeases    map[string]map[string]struct{}
	importLayerLocker      *locker.Locker

	mountPool sharableMountPool

	unlazyG flightcontrol.Group[struct{}]
}

func NewSnapshotManager(opt SnapshotManagerOpt) (SnapshotManager, error) {
	cm := &snapshotManager{
		Snapshotter:            snapshot.NewMergeSnapshotter(context.TODO(), opt.Snapshotter, opt.LeaseManager),
		ContentStore:           opt.ContentStore,
		LeaseManager:           opt.LeaseManager,
		Applier:                opt.Applier,
		Differ:                 opt.Differ,
		metadataStore:          newMetadataStore(),
		records:                make(map[string]*cacheRecord),
		snapshotContentDigests: make(map[string]map[digest.Digest]struct{}),
		importedLayerByBlob:    make(map[ImportedLayerBlobKey]string),
		importedLayerByDiff:    make(map[ImportedLayerDiffKey]string),
		snapshotOwnerLeases:    make(map[string]map[string]struct{}),
		importLayerLocker:      locker.New(),
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

// init loads all snapshots from metadata state and tries to load the records
// from the snapshotter. If snaphot can't be found, metadata is deleted as well.
func (cm *snapshotManager) init(ctx context.Context) error {
	_ = ctx
	// Snapshot metadata is in-memory only now; there is no persisted metadata DB to hydrate.
	return nil
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

	triggerUpdate := true
	for _, o := range opts {
		if o == NoUpdateLastUsed {
			triggerUpdate = false
		}
	}

	if rec.mutable {
		return nil, errors.Wrapf(errInvalid, "%s is mutable", id)
	}
	ref := &immutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: triggerUpdate,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}

// getRecord returns record for id. Requires manager lock.
func (cm *snapshotManager) getRecord(ctx context.Context, id string, opts ...RefOption) (cr *cacheRecord, retErr error) {
	if rec, ok := cm.records[id]; ok {
		if rec.isDead() {
			return nil, errors.Wrapf(errNotFound, "failed to get dead record %s", id)
		}
		if !rec.mutable {
			if _, err := cm.Snapshotter.Stat(ctx, rec.md.getSnapshotID()); err != nil {
				if !cerrdefs.IsNotFound(err) {
					return nil, errors.Wrapf(err, "failed to check immutable ref snapshot %s", rec.md.getSnapshotID())
				}
				if err := rec.remove(ctx); err != nil {
					return nil, errors.Wrap(err, "failed to remove immutable rec with missing snapshot")
				}
				return nil, errors.Wrap(errNotFound, rec.md.getSnapshotID())
			}
		}
		return rec, nil
	}

	md, ok := cm.getMetadata(id)
	if !ok {
		return nil, errors.Wrap(errNotFound, id)
	}

	rec := &cacheRecord{
		mutable: !md.getCommitted(),
		cm:      cm,
		md:      md,
	}

	// the record was deleted but we crashed before data on disk was removed
	if md.getDeleted() {
		if err := rec.remove(ctx); err != nil {
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
			if err := rec.remove(ctx); err != nil {
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
			if err := rec.remove(ctx); err != nil {
				return nil, err
			}
			return nil, errors.Wrapf(errNotFound, "failed to get record %s with dirty volatile overlay", id)
		}
	} else {
		if _, err := cm.Snapshotter.Stat(ctx, rec.md.getSnapshotID()); err != nil {
			if !cerrdefs.IsNotFound(err) {
				return nil, errors.Wrapf(err, "failed to check immutable ref snapshot %s", rec.md.getSnapshotID())
			}
			if err := rec.remove(ctx); err != nil {
				return nil, errors.Wrap(err, "failed to remove immutable rec with missing snapshot")
			}
			return nil, errors.Wrap(errNotFound, rec.md.getSnapshotID())
		}
	}

	if err := initializeMetadata(rec.md, opts...); err != nil {
		return nil, err
	}

	if err := setImageRefMetadata(rec.md, opts...); err != nil {
		return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", rec.ID())
	}

	cm.records[id] = rec
	return rec, nil
}

func (cm *snapshotManager) New(ctx context.Context, s ImmutableRef, opts ...RefOption) (mr MutableRef, err error) {
	id := identity.NewID()

	var parentSnapshotID string
	if s != nil {
		parentSnapshotID = s.SnapshotID()
	}

	snapshotID := id
	err = cm.Snapshotter.Prepare(ctx, snapshotID, parentSnapshotID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to prepare %v as %s", parentSnapshotID, snapshotID)
	}

	// All metadataStore and records map mutations must happen under cm.mu.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)

	rec := &cacheRecord{
		mutable: true,
		cm:      cm,
		md:      md,
	}

	opts = append(opts, withSnapshotID(snapshotID))
	if err := initializeMetadata(rec.md, opts...); err != nil {
		return nil, err
	}

	if err := setImageRefMetadata(rec.md, opts...); err != nil {
		return nil, errors.Wrapf(err, "failed to append image ref metadata to ref %s", rec.ID())
	}

	cm.records[id] = rec // TODO: save to db
	rec.locked = true
	ref := &mutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}

func (cm *snapshotManager) GetMutable(ctx context.Context, id string, opts ...RefOption) (MutableRef, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	rec, err := cm.getRecord(ctx, id, opts...)
	if err != nil {
		return nil, err
	}

	if !rec.mutable {
		return nil, errors.Wrapf(errInvalid, "%s is not mutable", id)
	}

	if rec.locked {
		return nil, errors.Wrapf(ErrLocked, "%s is locked", id)
	}
	rec.locked = true
	ref := &mutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
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

	if !rec.mutable {
		return nil, errors.Wrapf(errInvalid, "%s is not mutable", snapshotID)
	}
	if rec.locked {
		return nil, errors.Wrapf(ErrLocked, "%s is locked", snapshotID)
	}
	rec.locked = true
	ref := &mutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
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

	md := cm.ensureMetadata(snapshotID)
	if err := md.queueSnapshotID(snapshotID); err != nil {
		return err
	}
	if err := md.queueCommitted(committed); err != nil {
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

func (cm *snapshotManager) ApplySnapshotDiff(ctx context.Context, lower, upper ImmutableRef, opts ...RefOption) (ir ImmutableRef, rerr error) {
	switch {
	case lower == nil && upper == nil:
		return nil, nil
	case lower == nil:
		return cm.GetBySnapshotID(ctx, upper.SnapshotID(), append(opts, NoUpdateLastUsed)...)
	}

	id := identity.NewID()
	snapshotID := id

	var diffs []snapshot.Diff
	if upper == nil || lower.SnapshotID() != upper.SnapshotID() {
		diff := snapshot.Diff{
			Lower:      lower.SnapshotID(),
			Comparison: fsdiff.CompareContentOnMetadataMatch,
		}
		if upper != nil {
			diff.Upper = upper.SnapshotID()
		}
		diffs = append(diffs, diff)
	}
	if err := cm.Snapshotter.Merge(ctx, snapshotID, diffs); err != nil {
		return nil, errors.Wrap(err, "failed to apply snapshot diff")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)
	rec := &cacheRecord{
		mutable: false,
		cm:      cm,
		md:      md,
	}

	opts = append(opts, withSnapshotID(snapshotID))
	if err := initializeMetadata(rec.md, opts...); err != nil {
		return nil, err
	}
	if err := rec.md.queueSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	if err := rec.md.queueCommitted(true); err != nil {
		return nil, err
	}
	if err := rec.md.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec
	ref := &immutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}

func (cm *snapshotManager) Merge(ctx context.Context, parents []ImmutableRef, opts ...RefOption) (ir ImmutableRef, rerr error) {
	normalized := make([]ImmutableRef, 0, len(parents))
	for _, parent := range parents {
		if parent != nil {
			normalized = append(normalized, parent)
		}
	}

	switch len(normalized) {
	case 0:
		return nil, nil
	case 1:
		return cm.GetBySnapshotID(ctx, normalized[0].SnapshotID(), append(opts, NoUpdateLastUsed)...)
	}

	id := identity.NewID()
	snapshotID := id

	diffs := make([]snapshot.Diff, 0, len(normalized))
	for _, parent := range normalized {
		diffs = append(diffs, snapshot.Diff{Upper: parent.SnapshotID()})
	}
	if err := cm.Snapshotter.Merge(ctx, snapshotID, diffs); err != nil {
		return nil, errors.Wrap(err, "failed to merge snapshots")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)
	rec := &cacheRecord{
		mutable: false,
		cm:      cm,
		md:      md,
	}

	opts = append(opts, withSnapshotID(snapshotID))
	if err := initializeMetadata(rec.md, opts...); err != nil {
		return nil, err
	}
	if err := rec.md.queueSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	if err := rec.md.queueCommitted(true); err != nil {
		return nil, err
	}
	if err := rec.md.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec
	ref := &immutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}

type RefOption interface{}

type noUpdateLastUsed struct{}

var NoUpdateLastUsed noUpdateLastUsed

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

func initializeMetadata(m *cacheMetadata, opts ...RefOption) error {
	if tm := m.GetCreatedAt(); !tm.IsZero() {
		return nil
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
