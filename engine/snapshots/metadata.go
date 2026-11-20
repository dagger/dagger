package snapshots

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

const sizeUnknown int64 = -1
const keySize = "snapshot.size"
const keyDescription = "cache.description"
const keyCreatedAt = "cache.createdAt"
const keyLastUsedAt = "cache.lastUsedAt"
const keyUsageCount = "cache.usageCount"
const keyLayerType = "cache.layerType"
const keyRecordType = "cache.recordType"
const keyCommitted = "snapshot.committed"
const keyDiffID = "cache.diffID"
const keyBlob = "cache.blob"
const keySnapshot = "cache.snapshot"
const keyBlobOnly = "cache.blobonly"
const keyMediaType = "cache.mediatype"
const keyImageRefs = "cache.imageRefs"
const keyDeleted = "cache.deleted"
const keyBlobSize = "cache.blobsize" // packed blob size from the OCI descriptor
const keyURLs = "cache.layer.urls"

type MetadataStore interface {
	Search(context.Context, string, bool) ([]RefMetadata, error)
}

type RefMetadata interface {
	ID() string
	SnapshotID() string

	GetDescription() string
	SetDescription(string) error

	GetCreatedAt() time.Time
	SetCreatedAt(time.Time) error

	GetLayerType() string
	SetLayerType(string) error

	GetRecordType() client.UsageRecordType
	SetRecordType(client.UsageRecordType) error

	// generic getters/setters for external packages
	GetString(string) string
	Get(string) *Value
	SetString(key, val, index string) error

	GetExternal(string) ([]byte, error)
	SetExternal(string, []byte) error

	ClearValueAndIndex(string, string) error
}

type Value struct {
	Value json.RawMessage `json:"value,omitempty"`
	Index string          `json:"index,omitempty"`
}

func NewValue(v interface{}) (*Value, error) {
	dt, err := json.Marshal(v)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &Value{Value: json.RawMessage(dt)}, nil
}

func (v *Value) Unmarshal(target interface{}) error {
	if v == nil {
		return errors.New("nil metadata value")
	}
	return errors.WithStack(json.Unmarshal(v.Value, target))
}

type metadataStore struct {
	mu     sync.RWMutex
	refs   map[string]*cacheMetadata
	index  map[string]map[string]struct{}
	closed bool

	// Persisted hash records for snapshots whose metadata has not been opened.
	// With hashFiles, only those that could not be written to disk.
	pendingContentHashes map[string][]byte

	// hashFiles, when set, keeps the content-hash trees on disk instead of
	// in external and pendingContentHashes.
	hashFiles *contentHashFiles
	// contentHashMoves counts the trees moved between disk and memory, each
	// stored at its new place before it is dropped from the old one. A
	// lookup that finds a tree in neither place while one moved looks
	// again; see findContentHash.
	contentHashMoves atomic.Uint64
}

// maxContentHashLookups bounds findContentHash's lookups of a tree that keeps
// moving while it is looked up.
const maxContentHashLookups = 100

// findContentHash returns id's tree, kept on disk or, by inMemory, in memory.
//
// A tree moves between the two in two steps, stored at its new place before
// it is dropped from the old one, so looking in memory, then on disk, then in
// memory again finds a tree that one move overlaps. A lookup that finds it in
// no place while a move completed looks again, and a tree that kept moving
// for every lookup is an error, rather than no tree, which would make an
// imported snapshot's digests come from a rescan.
func (s *metadataStore) findContentHash(id string, inMemory func() ([]byte, bool), gone func() bool) ([]byte, bool, error) {
	for range maxContentHashLookups {
		moves := s.contentHashMoves.Load()
		if dt, ok := inMemory(); ok {
			return dt, true, nil
		}
		dt, ok, err := s.hashFiles.read(id, gone)
		if err != nil || ok {
			return dt, ok, err
		}
		if dt, ok := inMemory(); ok {
			return dt, true, nil
		}
		if s.contentHashMoves.Load() == moves {
			return nil, false, nil
		}
	}
	return nil, false, errors.Errorf("content hash records of %s kept moving between disk and memory", id)
}

func newMetadataStore() *metadataStore {
	return &metadataStore{
		refs:  make(map[string]*cacheMetadata),
		index: make(map[string]map[string]struct{}),
	}
}

// contentHashOnDisk says key's values are kept in hashFiles.
func (s *metadataStore) contentHashOnDisk(key string) bool {
	return s.hashFiles != nil && key == ContentHashMetadataKey
}

func (s *metadataStore) get(id string) (*cacheMetadata, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	md, ok := s.refs[id]
	return md, ok
}

func (s *metadataStore) getOrCreate(id string) *cacheMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	if md, ok := s.refs[id]; ok {
		return md
	}
	md := &cacheMetadata{
		id:       id,
		store:    s,
		values:   make(map[string]*Value),
		indexes:  make(map[string]string),
		external: make(map[string][]byte),
	}
	if data, ok := s.pendingContentHashes[id]; ok {
		md.external[ContentHashMetadataKey] = data
		delete(s.pendingContentHashes, id)
	}
	s.refs[id] = md
	return md
}

func (s *metadataStore) clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingContentHashes, id)
	md, ok := s.refs[id]
	if s.hashFiles != nil {
		s.hashFiles.remove(id, func() {
			if ok {
				md.contentHashGone = true
			}
		})
	}
	if !ok {
		return
	}
	for _, idx := range md.indexes {
		if idx == "" {
			continue
		}
		set, ok := s.index[idx]
		if !ok {
			continue
		}
		delete(set, id)
		if len(set) == 0 {
			delete(s.index, idx)
		}
	}
	delete(s.refs, id)
}

// CollectedSnapshotForgetter is a snapshot manager that keeps metadata for
// snapshots, and can drop it once the snapshots are gone.
type CollectedSnapshotForgetter interface {
	// ForgetCollectedSnapshots drops the metadata of the snapshots the
	// snapshotter no longer has: what is kept in memory, and their
	// content-hash trees, in memory or on disk. Run it after garbage
	// collection.
	ForgetCollectedSnapshots(ctx context.Context) error
}

// ForgetCollectedSnapshots drops what the manager keeps in memory about
// snapshots that garbage collection removed: their records, metadata and
// loaded hash records, their recorded blobs, and the imported layers that
// name them. Nothing else tells the manager a snapshot is gone: only a later
// Get of it notices, and most collected snapshots are never asked for again,
// so without this their metadata would stay in memory, and the serialized
// content-hash tree each hashed snapshot keeps in memory or on disk would
// stay there, for the life of the engine, and the blob rows in every
// checkpoint.
//
// Every snapshot ID is a fresh random ID, or that of a snapshot that exists
// when its metadata is made, and every snapshot is made before its metadata
// or under cm.mu; blobs and imported layers are recorded for snapshots that
// exist. So an entry present before the walk whose snapshot the walk does
// not see is gone for good, and the walk runs without cm.mu.
func (cm *snapshotManager) ForgetCollectedSnapshots(ctx context.Context) error {
	cm.mu.Lock()
	cm.metadataStore.mu.RLock()
	candidates := make(map[string]struct{}, len(cm.metadataStore.refs)+len(cm.metadataStore.pendingContentHashes))
	for id := range cm.metadataStore.refs {
		candidates[id] = struct{}{}
	}
	for id := range cm.metadataStore.pendingContentHashes {
		candidates[id] = struct{}{}
	}
	if cm.metadataStore.hashFiles != nil {
		for _, id := range cm.metadataStore.hashFiles.list() {
			candidates[id] = struct{}{}
		}
	}
	cm.metadataStore.mu.RUnlock()
	for id := range cm.records {
		candidates[id] = struct{}{}
	}
	for id := range cm.snapshotContentDigests {
		candidates[id] = struct{}{}
	}
	for id := range cm.snapshotContentOwned {
		candidates[id] = struct{}{}
	}
	for _, id := range cm.importedLayerByBlob {
		candidates[id] = struct{}{}
	}
	for _, id := range cm.importedLayerByDiff {
		candidates[id] = struct{}{}
	}
	cm.mu.Unlock()
	if len(candidates) == 0 {
		return nil
	}

	existing := make(map[string]struct{}, len(candidates))
	if err := cm.Snapshotter.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if _, ok := candidates[info.Name]; ok {
			existing[info.Name] = struct{}{}
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "walk snapshots")
	}

	gone := func(id string) bool {
		_, candidate := candidates[id]
		_, exists := existing[id]
		return candidate && !exists
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	for key, id := range cm.importedLayerByBlob {
		if gone(id) {
			delete(cm.importedLayerByBlob, key)
		}
	}
	for key, id := range cm.importedLayerByDiff {
		if gone(id) {
			delete(cm.importedLayerByDiff, key)
		}
	}
	for id := range candidates {
		if _, ok := existing[id]; ok {
			continue
		}
		delete(cm.snapshotContentDigests, id)
		delete(cm.snapshotContentOwned, id)
		if rec, ok := cm.records[id]; ok {
			if rec.mutable && rec.locked {
				// In use: its holder finds the snapshot gone.
				continue
			}
			if sid := rec.md.getSnapshotID(); sid != id {
				// Not keyed by its snapshot; leave it to getRecord.
				continue
			}
			delete(cm.records, id)
		} else if md, ok := cm.metadataStore.get(id); ok && md.getSnapshotID() != id {
			continue
		}
		cm.metadataStore.clear(id)
	}
	return nil
}

func (s *metadataStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.refs = nil
	s.index = nil
	s.pendingContentHashes = nil
	if s.hashFiles != nil {
		s.hashFiles.close()
	}
	return nil
}

type cacheMetadata struct {
	id       string
	store    *metadataStore
	values   map[string]*Value
	indexes  map[string]string // key -> index
	external map[string][]byte
	queue    []func(*cacheMetadata) error
	// contentHashGone is set, under store.hashFiles.mu, once the metadata is
	// dropped: a tree saved for it later is discarded.
	contentHashGone bool
}

func (cm *snapshotManager) Search(ctx context.Context, idx string, prefixOnly bool) ([]RefMetadata, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.search(idx, prefixOnly), nil
}

// callers must hold cm.mu lock
func (cm *snapshotManager) search(idx string, prefixOnly bool) []RefMetadata {
	ids := map[string]struct{}{}
	cm.metadataStore.mu.RLock()
	for indexedVal, set := range cm.metadataStore.index {
		if prefixOnly {
			if !strings.HasPrefix(indexedVal, idx) {
				continue
			}
		} else if indexedVal != idx {
			continue
		}
		for id := range set {
			ids[id] = struct{}{}
		}
	}
	cm.metadataStore.mu.RUnlock()
	orderedIDs := make([]string, 0, len(ids))
	for id := range ids {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Strings(orderedIDs)

	mds := make([]RefMetadata, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		md, ok := cm.getMetadata(id)
		if !ok || md.getDeleted() {
			continue
		}
		mds = append(mds, md)
	}
	return mds
}

// callers must hold cm.mu lock
func (cm *snapshotManager) getMetadata(id string) (*cacheMetadata, bool) {
	if rec, ok := cm.records[id]; ok {
		return rec.md, true
	}
	return cm.metadataStore.get(id)
}

// callers must hold cm.mu lock
func (cm *snapshotManager) ensureMetadata(id string) *cacheMetadata {
	if rec, ok := cm.records[id]; ok {
		return rec.md
	}
	return cm.metadataStore.getOrCreate(id)
}

func (md *cacheMetadata) ID() string {
	return md.id
}

func (md *cacheMetadata) SnapshotID() string {
	return md.getSnapshotID()
}

func (md *cacheMetadata) commitMetadata() error {
	md.store.mu.Lock()
	defer md.store.mu.Unlock()

	if len(md.queue) == 0 {
		return nil
	}
	q := md.queue
	md.queue = nil
	for _, fn := range q {
		if err := fn(md); err != nil {
			return err
		}
	}
	return nil
}

// callers must hold metadataStore.mu lock
func (md *cacheMetadata) setValueLocked(key string, v *Value) {
	oldIdx := md.indexes[key]
	if oldIdx != "" {
		if set, ok := md.store.index[oldIdx]; ok {
			delete(set, md.id)
			if len(set) == 0 {
				delete(md.store.index, oldIdx)
			}
		}
	}

	if v == nil {
		delete(md.values, key)
		delete(md.indexes, key)
		return
	}

	md.values[key] = v
	md.indexes[key] = v.Index
	if v.Index != "" {
		set, ok := md.store.index[v.Index]
		if !ok {
			set = make(map[string]struct{})
			md.store.index[v.Index] = set
		}
		set[md.id] = struct{}{}
	}
}

func (md *cacheMetadata) GetDescription() string {
	return md.GetString(keyDescription)
}

func (md *cacheMetadata) SetDescription(descr string) error {
	return md.setValue(keyDescription, descr, "")
}

func (md *cacheMetadata) queueDescription(descr string) error {
	return md.queueValue(keyDescription, descr, "")
}

func (md *cacheMetadata) queueCommitted(b bool) error {
	return md.queueValue(keyCommitted, b, "")
}

func (md *cacheMetadata) getCommitted() bool {
	return md.getBool(keyCommitted)
}

func (md *cacheMetadata) GetLayerType() string {
	return md.GetString(keyLayerType)
}

func (md *cacheMetadata) SetLayerType(value string) error {
	return md.setValue(keyLayerType, value, "")
}

func (md *cacheMetadata) GetRecordType() client.UsageRecordType {
	return client.UsageRecordType(md.GetString(keyRecordType))
}

func (md *cacheMetadata) SetRecordType(value client.UsageRecordType) error {
	return md.setValue(keyRecordType, value, "")
}

func (md *cacheMetadata) queueRecordType(value client.UsageRecordType) error {
	return md.queueValue(keyRecordType, value, "")
}

func (md *cacheMetadata) SetCreatedAt(tm time.Time) error {
	return md.setTime(keyCreatedAt, tm, "")
}

func (md *cacheMetadata) queueCreatedAt(tm time.Time) error {
	return md.queueTime(keyCreatedAt, tm, "")
}

func (md *cacheMetadata) GetCreatedAt() time.Time {
	return md.getTime(keyCreatedAt)
}

// ErrExternalNotFound is GetExternal's error for a key with no value. Any
// other error is a failure to read the value.
var ErrExternalNotFound = errors.New("not found")

func (md *cacheMetadata) GetExternal(s string) ([]byte, error) {
	if md.store.contentHashOnDisk(s) {
		// Kept on disk, or in memory when it couldn't be written.
		dt, ok, err := md.store.findContentHash(md.id,
			func() ([]byte, bool) { return md.getExternalInMemory(s) },
			func() bool { return md.contentHashGone })
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrExternalNotFound
		}
		return dt, nil
	}
	if dt, ok := md.getExternalInMemory(s); ok {
		return dt, nil
	}
	return nil, ErrExternalNotFound
}

func (md *cacheMetadata) getExternalInMemory(s string) ([]byte, bool) {
	md.store.mu.RLock()
	defer md.store.mu.RUnlock()
	dt, ok := md.external[s]
	if !ok {
		return nil, false
	}
	return bytes.Clone(dt), true
}

func (md *cacheMetadata) setExternalInMemory(s string, dt []byte) {
	md.store.mu.Lock()
	defer md.store.mu.Unlock()
	md.external[s] = bytes.Clone(dt)
}

func (md *cacheMetadata) SetExternal(s string, dt []byte) error {
	if md.store.contentHashOnDisk(s) {
		err := md.store.hashFiles.write(md.id, dt, func() bool { return md.contentHashGone })
		if err == nil {
			// The tree on disk replaces one kept in memory before: a move,
			// stored on disk before it is dropped from memory.
			md.store.mu.Lock()
			_, moved := md.external[s]
			delete(md.external, s)
			md.store.mu.Unlock()
			if moved {
				md.store.contentHashMoves.Add(1)
			}
			return nil
		}
		// Keep the tree in memory, where trees were kept before, as loading
		// one that can't be written does, rather than fail the save: a host
		// sync would fail with it, and a background save would lose it. The
		// tree on disk, if any, is older: drop it once this one is in
		// memory, so this one is read and checkpointed. The next save tries
		// the disk again.
		bklog.G(context.TODO()).WithError(err).WithField("snapshot", md.id).
			Error("failed to write content hash records; keeping them in memory")
		md.setExternalInMemory(s, dt)
		md.store.hashFiles.remove(md.id, nil)
		md.store.contentHashMoves.Add(1)
		return nil
	}
	md.setExternalInMemory(s, dt)
	return nil
}

func (md *cacheMetadata) queueDiffID(str digest.Digest) error {
	return md.queueValue(keyDiffID, str, "")
}

func (md *cacheMetadata) getMediaType() string {
	return md.GetString(keyMediaType)
}

func (md *cacheMetadata) queueMediaType(str string) error {
	return md.queueValue(keyMediaType, str, "")
}

func (md *cacheMetadata) getSnapshotID() string {
	sid := md.GetString(keySnapshot)
	if sid == "" {
		return md.ID()
	}
	return sid
}

func (md *cacheMetadata) queueSnapshotID(str string) error {
	return md.queueValue(keySnapshot, str, "")
}

func (md *cacheMetadata) getDiffID() digest.Digest {
	return digest.Digest(md.GetString(keyDiffID))
}

func (md *cacheMetadata) queueBlob(str digest.Digest) error {
	return md.queueValue(keyBlob, str, "")
}

func (md *cacheMetadata) appendURLs(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	return md.appendStringSlice(keyURLs, urls...)
}

func (md *cacheMetadata) getURLs() []string {
	return md.GetStringSlice(keyURLs)
}

func (md *cacheMetadata) getBlob() digest.Digest {
	return digest.Digest(md.GetString(keyBlob))
}

func (md *cacheMetadata) queueBlobOnly(b bool) error {
	return md.queueValue(keyBlobOnly, b, "")
}

func (md *cacheMetadata) getBlobOnly() bool {
	return md.getBool(keyBlobOnly)
}

func (md *cacheMetadata) getDeleted() bool {
	return md.getBool(keyDeleted)
}

func (md *cacheMetadata) queueSize(s int64) error {
	return md.queueValue(keySize, s, "")
}

func (md *cacheMetadata) getSize() int64 {
	if size, ok := md.getInt64(keySize); ok {
		return size
	}
	return sizeUnknown
}

func (md *cacheMetadata) appendImageRef(s string) error {
	return md.appendStringSlice(keyImageRefs, s)
}

func (md *cacheMetadata) getImageRefs() []string {
	return md.getStringSlice(keyImageRefs)
}

func (md *cacheMetadata) queueBlobSize(s int64) error {
	return md.queueValue(keyBlobSize, s, "")
}

func (md *cacheMetadata) getBlobSize() int64 {
	if size, ok := md.getInt64(keyBlobSize); ok {
		return size
	}
	return sizeUnknown
}

func (md *cacheMetadata) getLastUsed() (int, *time.Time) {
	v := md.Get(keyUsageCount)
	if v == nil {
		return 0, nil
	}
	var usageCount int
	if err := v.Unmarshal(&usageCount); err != nil {
		return 0, nil
	}
	v = md.Get(keyLastUsedAt)
	if v == nil {
		return usageCount, nil
	}
	var lastUsedTS int64
	if err := v.Unmarshal(&lastUsedTS); err != nil || lastUsedTS == 0 {
		return usageCount, nil
	}
	tm := time.Unix(lastUsedTS/1e9, lastUsedTS%1e9)
	return usageCount, &tm
}

func (md *cacheMetadata) updateLastUsed() error {
	count, _ := md.getLastUsed()
	count++
	if err := md.setValue(keyUsageCount, count, ""); err != nil {
		return err
	}
	return md.setValue(keyLastUsedAt, time.Now().UnixNano(), "")
}

func (md *cacheMetadata) queueValue(key string, value interface{}, index string) error {
	v, err := NewValue(value)
	if err != nil {
		return errors.Wrap(err, "failed to create value")
	}
	v.Index = index

	md.store.mu.Lock()
	defer md.store.mu.Unlock()
	md.queue = append(md.queue, func(md *cacheMetadata) error {
		md.setValueLocked(key, v)
		return nil
	})
	return nil
}

func (md *cacheMetadata) SetString(key, value string, index string) error {
	return md.setValue(key, value, index)
}

func (md *cacheMetadata) setValue(key string, value interface{}, index string) error {
	v, err := NewValue(value)
	if err != nil {
		return errors.Wrap(err, "failed to create value")
	}
	v.Index = index

	md.store.mu.Lock()
	defer md.store.mu.Unlock()
	md.setValueLocked(key, v)
	return nil
}

func (md *cacheMetadata) ClearValueAndIndex(key string, index string) error {
	md.store.mu.Lock()
	defer md.store.mu.Unlock()

	currentVal := ""
	if v := md.values[key]; v != nil {
		var str string
		if err := v.Unmarshal(&str); err == nil {
			currentVal = str
		}
	}
	md.setValueLocked(key, nil)
	if currentVal != "" {
		idx := index + currentVal
		if set, ok := md.store.index[idx]; ok {
			delete(set, md.ID())
			if len(set) == 0 {
				delete(md.store.index, idx)
			}
		}
	}
	return nil
}

func (md *cacheMetadata) GetString(key string) string {
	v := md.Get(key)
	if v == nil {
		return ""
	}
	var str string
	if err := v.Unmarshal(&str); err != nil {
		return ""
	}
	return str
}

func (md *cacheMetadata) Get(key string) *Value {
	md.store.mu.RLock()
	defer md.store.mu.RUnlock()
	v := md.values[key]
	if v == nil {
		return nil
	}
	copyVal := *v
	return &copyVal
}

func (md *cacheMetadata) GetStringSlice(key string) []string {
	v := md.Get(key)
	if v == nil {
		return nil
	}
	var val []string
	if err := v.Unmarshal(&val); err != nil {
		return nil
	}
	return val
}

func (md *cacheMetadata) setTime(key string, value time.Time, index string) error {
	return md.setValue(key, value.UnixNano(), index)
}

func (md *cacheMetadata) queueTime(key string, value time.Time, index string) error {
	return md.queueValue(key, value.UnixNano(), index)
}

func (md *cacheMetadata) getTime(key string) time.Time {
	v := md.Get(key)
	if v == nil {
		return time.Time{}
	}
	var tm int64
	if err := v.Unmarshal(&tm); err != nil {
		return time.Time{}
	}
	return time.Unix(tm/1e9, tm%1e9)
}

func (md *cacheMetadata) getBool(key string) bool {
	v := md.Get(key)
	if v == nil {
		return false
	}
	var b bool
	if err := v.Unmarshal(&b); err != nil {
		return false
	}
	return b
}

func (md *cacheMetadata) getInt64(key string) (int64, bool) {
	v := md.Get(key)
	if v == nil {
		return 0, false
	}
	var i int64
	if err := v.Unmarshal(&i); err != nil {
		return 0, false
	}
	return i, true
}

func (md *cacheMetadata) appendStringSlice(key string, values ...string) error {
	slice := md.GetStringSlice(key)
	idx := make(map[string]struct{}, len(values))
	for _, v := range values {
		idx[v] = struct{}{}
	}
	for _, existing := range slice {
		delete(idx, existing)
	}
	if len(idx) == 0 {
		return nil
	}
	for value := range idx {
		slice = append(slice, value)
	}
	return md.setValue(key, slice, "")
}

func (md *cacheMetadata) getStringSlice(key string) []string {
	return md.GetStringSlice(key)
}
