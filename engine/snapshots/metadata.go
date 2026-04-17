package snapshots

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/buildkit/client"
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
}

func newMetadataStore() *metadataStore {
	return &metadataStore{
		refs:  make(map[string]*cacheMetadata),
		index: make(map[string]map[string]struct{}),
	}
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
	s.refs[id] = md
	return md
}

func (s *metadataStore) clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	md, ok := s.refs[id]
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

func (s *metadataStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.refs = nil
	s.index = nil
	return nil
}

type cacheMetadata struct {
	id       string
	store    *metadataStore
	values   map[string]*Value
	indexes  map[string]string // key -> index
	external map[string][]byte
	queue    []func(*cacheMetadata) error
}

func (cm *snapshotManager) Search(ctx context.Context, idx string, prefixOnly bool) ([]RefMetadata, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.search(ctx, idx, prefixOnly)
}

// callers must hold cm.mu lock
func (cm *snapshotManager) search(_ context.Context, idx string, prefixOnly bool) ([]RefMetadata, error) {
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
	return mds, nil
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

func (md *cacheMetadata) GetExternal(s string) ([]byte, error) {
	md.store.mu.RLock()
	defer md.store.mu.RUnlock()
	dt, ok := md.external[s]
	if !ok {
		return nil, errors.New("not found")
	}
	cpy := make([]byte, len(dt))
	copy(cpy, dt)
	return cpy, nil
}

func (md *cacheMetadata) SetExternal(s string, dt []byte) error {
	md.store.mu.Lock()
	defer md.store.mu.Unlock()
	cpy := make([]byte, len(dt))
	copy(cpy, dt)
	md.external[s] = cpy
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

func (md *cacheMetadata) queueDeleted() error {
	return md.queueValue(keyDeleted, true, "")
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
