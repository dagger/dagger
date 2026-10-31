// Package bbolt is an in-memory, speed-first replacement for go.etcd.io/bbolt
// with the subset of its API that the engine and containerd's metadata and
// snapshot stores use. It is swapped in for every caller by a go.mod replace.
//
// Semantics follow bbolt where callers can observe them: one writer at a time,
// readers that never wait for the writer and see the state as of their Begin,
// nested buckets, keys in byte order, and changes that become visible only on
// Commit and vanish on Rollback.
//
// Nothing is durable while the process runs. Close writes the whole database
// to its file, and Open reads it back, as the dagql cache does with its own
// state; see persist.go. A database that was opened and never closed is
// refused at the next Open, so the engine's local-cache reset takes over.
//
// Each bucket is a persistent treap. A write transaction copies the path to
// every node it changes; nodes it created itself it changes in place. Commit
// publishes the new root with one atomic store, so readers take no lock.
//
// Allocation is kept low on the paths containerd runs most: a transaction and
// its root bucket are one object; a read transaction's handles to a bucket are
// one handle, shared by every reader of that version; a write transaction's
// come from per-transaction chunks; a key and its value share one allocation;
// and handles reuse the store's own copy of a bucket's name.
//
// One difference from bbolt follows: Tx returns nil on a bucket opened in a
// read transaction, which no caller in containerd or the engine uses.
package bbolt

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt/errors"
)

const (
	// MaxKeySize is the maximum length of a key, in bytes.
	MaxKeySize = 32768
	// MaxValueSize is the maximum length of a value, in bytes.
	MaxValueSize = (1 << 31) - 2
)

// FreelistType is accepted for compatibility and ignored.
type FreelistType string

const (
	FreelistArrayType = FreelistType("array")
	FreelistMapType   = FreelistType("hashmap")
)

// Options is accepted for compatibility; every field is ignored.
type Options struct {
	Timeout         time.Duration
	NoGrowSync      bool
	NoFreelistSync  bool
	PreLoadFreelist bool
	FreelistType    FreelistType
	ReadOnly        bool
	MmapFlags       int
	InitialMmapSize int
	PageSize        int
	MaxSize         int
	NoSync          bool
	OpenFile        func(string, int, os.FileMode) (*os.File, error)
	Mlock           bool
	Logger          Logger
	NoStatistics    bool
}

// Logger is accepted for compatibility and never called.
type Logger interface {
	Debug(v ...interface{})
	Debugf(format string, v ...interface{})
	Error(v ...interface{})
	Errorf(format string, v ...interface{})
	Info(v ...interface{})
	Infof(format string, v ...interface{})
	Warning(v ...interface{})
	Warningf(format string, v ...interface{})
	Fatal(v ...interface{})
	Fatalf(format string, v ...interface{})
	Panic(v ...interface{})
	Panicf(format string, v ...interface{})
}

var DefaultOptions = &Options{
	Timeout:      0,
	NoGrowSync:   false,
	FreelistType: FreelistArrayType,
}

// Stats is accepted for compatibility and always zero.
type Stats struct{}

// node is one entry of a bucket's treap: a value, or a nested bucket. The key
// and the value share kv: the key is kv[:klen], the value kv[klen:]. A bucket
// entry's kv is just its key.
type node struct {
	kv          []byte
	klen        uint32
	prio        uint32
	bkt         *bucketData
	gen         uint64
	left, right *node
}

func (n *node) key() []byte { return n.kv[:n.klen:n.klen] }
func (n *node) val() []byte { return n.kv[n.klen:] }

// bucketData is one version of a bucket. A write transaction changes in place
// only data and nodes whose gen is its own. Every version of one bucket has
// the same id, and no other bucket has it.
type bucketData struct {
	root *node
	seq  uint64
	gen  uint64
	id   uint64
	// ro is the read-only handle every read transaction shares for this
	// version, made by the first to open it. Only committed versions get one.
	ro atomic.Pointer[Bucket]
}

func prio(key []byte) uint32 {
	// FNV-1a, then a finalizer: a fixed pseudo-random priority per key keeps
	// the treap balanced in expectation without a random source.
	h := uint32(2166136261)
	for _, c := range key {
		h ^= uint32(c)
		h *= 16777619
	}
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	return h
}

func own(n *node, gen uint64) *node {
	if n.gen == gen {
		return n
	}
	c := *n
	c.gen = gen
	return &c
}

func get(n *node, key []byte) *node {
	for n != nil {
		switch c := bytes.Compare(key, n.key()); {
		case c < 0:
			n = n.left
		case c > 0:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// upsert sets key's entry to kv (with klen) and bkt, copying the path from n
// to it. An entry that already exists keeps its priority and children; one
// the write transaction owns already changes in place.
func upsert(n *node, key, kv []byte, klen uint32, bkt *bucketData, p uint32, gen uint64) *node {
	if n == nil {
		return &node{kv: kv, klen: klen, prio: p, bkt: bkt, gen: gen}
	}
	switch c := bytes.Compare(key, n.key()); {
	case c == 0:
		n = own(n, gen)
		n.kv, n.klen, n.bkt = kv, klen, bkt
		return n
	case c < 0:
		n = own(n, gen)
		n.left = upsert(n.left, key, kv, klen, bkt, p, gen)
		if n.left.prio > n.prio {
			l := n.left
			n.left = l.right
			l.right = n
			return l
		}
		return n
	default:
		n = own(n, gen)
		n.right = upsert(n.right, key, kv, klen, bkt, p, gen)
		if n.right.prio > n.prio {
			r := n.right
			n.right = r.left
			r.left = n
			return r
		}
		return n
	}
}

func merge(a, b *node, gen uint64) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a = own(a, gen)
		a.right = merge(a.right, b, gen)
		return a
	}
	b = own(b, gen)
	b.left = merge(a, b.left, gen)
	return b
}

// remove deletes key, which the caller has found under n.
func remove(n *node, key []byte, gen uint64) *node {
	switch c := bytes.Compare(key, n.key()); {
	case c == 0:
		return merge(n.left, n.right, gen)
	case c < 0:
		n = own(n, gen)
		n.left = remove(n.left, key, gen)
		return n
	default:
		n = own(n, gen)
		n.right = remove(n.right, key, gen)
		return n
	}
}

func first(n *node) *node {
	if n == nil {
		return nil
	}
	for n.left != nil {
		n = n.left
	}
	return n
}

func last(n *node) *node {
	if n == nil {
		return nil
	}
	for n.right != nil {
		n = n.right
	}
	return n
}

// ceil is the first node with a key >= key; with strict, the first > key.
func ceil(n *node, key []byte, strict bool) *node {
	var best *node
	for n != nil {
		c := bytes.Compare(n.key(), key)
		if c > 0 || (c == 0 && !strict) {
			best = n
			n = n.left
		} else {
			n = n.right
		}
	}
	return best
}

// before is the last node with a key < key.
func before(n *node, key []byte) *node {
	var best *node
	for n != nil {
		if bytes.Compare(n.key(), key) < 0 {
			best = n
			n = n.right
		} else {
			n = n.left
		}
	}
	return best
}

// walk calls fn on every node under n in key order, stopping at its first
// error. Its stack lives on the goroutine's stack for any balanced treap.
func walk(n *node, fn func(*node) error) error {
	var buf [64]*node
	stack := buf[:0]
	for n != nil || len(stack) > 0 {
		for n != nil {
			stack = append(stack, n)
			n = n.left
		}
		n = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if err := fn(n); err != nil {
			return err
		}
		n = n.right
	}
	return nil
}

// DB is an in-memory database.
type DB struct {
	path   string
	mode   os.FileMode
	wmu    sync.Mutex
	root   atomic.Pointer[bucketData]
	gen    uint64 // guarded by wmu
	ids    uint64 // the last bucket id given out; guarded by wmu
	closed atomic.Bool
}

func (db *DB) Path() string     { return db.path }
func (db *DB) GoString() string { return fmt.Sprintf("bolt.DB{path:%q}", db.path) }
func (db *DB) String() string   { return fmt.Sprintf("DB<%q>", db.path) }
func (db *DB) IsReadOnly() bool { return false }
func (db *DB) Sync() error      { return nil }
func (db *DB) Stats() Stats     { return Stats{} }

func (db *DB) Begin(writable bool) (*Tx, error) {
	if db.closed.Load() {
		return nil, errors.ErrDatabaseNotOpen
	}
	tx := &Tx{db: db, writable: writable}
	if writable {
		db.wmu.Lock()
		if db.closed.Load() {
			db.wmu.Unlock()
			return nil, errors.ErrDatabaseNotOpen
		}
		db.gen++
		tx.gen = db.gen
	}
	tx.root = Bucket{tx: tx, data: db.root.Load()}
	return tx, nil
}

func (db *DB) Update(fn func(*Tx) error) error {
	tx, err := db.Begin(true)
	if err != nil {
		return err
	}
	defer func() {
		if !tx.done {
			tx.managed = false
			_ = tx.Rollback()
		}
	}()
	tx.managed = true
	err = fn(tx)
	tx.managed = false
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (db *DB) View(fn func(*Tx) error) error {
	tx, err := db.Begin(false)
	if err != nil {
		return err
	}
	defer func() {
		if !tx.done {
			tx.managed = false
			_ = tx.Rollback()
		}
	}()
	tx.managed = true
	err = fn(tx)
	tx.managed = false
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Rollback()
}

// Batch runs fn in its own write transaction.
func (db *DB) Batch(fn func(*Tx) error) error { return db.Update(fn) }

// Tx is a transaction.
type Tx struct {
	db             *DB
	writable       bool
	managed        bool
	done           bool
	gen            uint64
	root           Bucket
	handles        []Bucket
	commitHandlers []func()
	// relinks counts the times this write transaction changed which version
	// of a bucket its parent holds. A handle that saw an older count looks its
	// bucket up again before use.
	relinks uint64
}

func (tx *Tx) ID() int        { return int(tx.gen) }
func (tx *Tx) DB() *DB        { return tx.db }
func (tx *Tx) Size() int64    { return 0 }
func (tx *Tx) Writable() bool { return tx.writable }
func (tx *Tx) Cursor() *Cursor {
	return tx.root.Cursor()
}
func (tx *Tx) Bucket(name []byte) *Bucket { return tx.root.Bucket(name) }
func (tx *Tx) CreateBucket(name []byte) (*Bucket, error) {
	return tx.root.CreateBucket(name)
}
func (tx *Tx) CreateBucketIfNotExists(name []byte) (*Bucket, error) {
	return tx.root.CreateBucketIfNotExists(name)
}
func (tx *Tx) DeleteBucket(name []byte) error { return tx.root.DeleteBucket(name) }
func (tx *Tx) ForEach(fn func(name []byte, b *Bucket) error) error {
	return tx.root.ForEachBucket(func(k []byte) error {
		return fn(k, tx.root.Bucket(k))
	})
}
func (tx *Tx) OnCommit(fn func()) { tx.commitHandlers = append(tx.commitHandlers, fn) }

// handle returns a bucket handle from the transaction's current chunk; each
// chunk is twice the last, up to 32 handles.
func (tx *Tx) handle() *Bucket {
	if len(tx.handles) == cap(tx.handles) {
		tx.handles = make([]Bucket, 0, min(max(2*cap(tx.handles), 4), 32))
	}
	tx.handles = tx.handles[:len(tx.handles)+1]
	return &tx.handles[len(tx.handles)-1]
}

func (tx *Tx) Commit() error {
	if tx.managed {
		panic("managed tx commit not allowed")
	}
	if tx.done {
		return errors.ErrTxClosed
	}
	if !tx.writable {
		return errors.ErrTxNotWritable
	}
	tx.db.root.Store(tx.root.data)
	tx.done = true
	tx.db.wmu.Unlock()
	for _, fn := range tx.commitHandlers {
		fn()
	}
	return nil
}

func (tx *Tx) Rollback() error {
	if tx.managed {
		panic("managed tx rollback not allowed")
	}
	if tx.done {
		return errors.ErrTxClosed
	}
	tx.done = true
	if tx.writable {
		tx.db.wmu.Unlock()
	}
	return nil
}

// Bucket is a handle to one bucket within one transaction. A write
// transaction may hold several handles to one bucket: each finds the bucket's
// latest version again whenever the transaction has relinked a bucket since
// the handle last looked, so every handle sees every write.
type Bucket struct {
	tx     *Tx
	data   *bucketData
	parent *Bucket
	name   []byte // the store's own copy, shared with the parent's node
	seen   uint64 // tx.relinks when data was last known to be current
	// detached is set once the bucket is gone from its parent: deleted, or
	// replaced by another of the same name. Its writes then go nowhere, as a
	// deleted bucket's do in bbolt.
	detached bool
}

// Tx is the bucket's transaction, or nil for a bucket opened in a read
// transaction.
func (b *Bucket) Tx() *Tx        { return b.tx }
func (b *Bucket) Writable() bool { return b.tx != nil && b.tx.writable }

// cur returns the bucket's latest version in this transaction.
func (b *Bucket) cur() *bucketData {
	if b.tx != nil && b.seen != b.tx.relinks {
		b.seen = b.tx.relinks
		if b.parent != nil && !b.detached {
			n := get(b.parent.cur().root, b.name)
			if n != nil && n.bkt != nil && n.bkt.id == b.data.id {
				b.data = n.bkt
			} else {
				b.detached = true
			}
		}
	}
	return b.data
}

// mutable makes b's data owned by the write transaction, linking the new
// version into its parent.
func (b *Bucket) mutable() *bucketData {
	d := b.cur()
	if d.gen == b.tx.gen {
		return d
	}
	d = &bucketData{root: d.root, seq: d.seq, gen: b.tx.gen, id: d.id}
	b.data = d
	if b.parent != nil && !b.detached {
		pd := b.parent.mutable()
		pd.root = upsert(pd.root, b.name, b.name, uint32(len(b.name)), d, prio(b.name), b.tx.gen)
		b.tx.relinks++
		// b and its ancestors hold the versions just linked.
		for a := b; a != nil; a = a.parent {
			a.seen = b.tx.relinks
		}
	}
	return d
}

func (b *Bucket) open(n *node) *Bucket {
	if b.tx == nil || !b.tx.writable {
		if h := n.bkt.ro.Load(); h != nil {
			return h
		}
		h := &Bucket{data: n.bkt, name: n.key()}
		if n.bkt.ro.CompareAndSwap(nil, h) {
			return h
		}
		return n.bkt.ro.Load()
	}
	c := b.tx.handle()
	*c = Bucket{tx: b.tx, data: n.bkt, parent: b, name: n.key(), seen: b.tx.relinks}
	return c
}

func (b *Bucket) Bucket(name []byte) *Bucket {
	n := get(b.cur().root, name)
	if n == nil || n.bkt == nil {
		return nil
	}
	return b.open(n)
}

func (b *Bucket) checkWrite() error {
	if b.tx == nil {
		return errors.ErrTxNotWritable
	}
	if b.tx.done {
		return errors.ErrTxClosed
	}
	if !b.tx.writable {
		return errors.ErrTxNotWritable
	}
	return nil
}

func (b *Bucket) CreateBucket(key []byte) (*Bucket, error) {
	if err := b.checkWrite(); err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, errors.ErrBucketNameRequired
	}
	if n := get(b.cur().root, key); n != nil {
		if n.bkt != nil {
			return nil, errors.ErrBucketExists
		}
		return nil, errors.ErrIncompatibleValue
	}
	return b.create(key), nil
}

func (b *Bucket) create(key []byte) *Bucket {
	d := b.mutable()
	b.tx.db.ids++
	nd := &bucketData{gen: b.tx.gen, id: b.tx.db.ids}
	k := bytes.Clone(key)
	d.root = upsert(d.root, k, k, uint32(len(k)), nd, prio(k), b.tx.gen)
	b.tx.relinks++
	b.seen = b.tx.relinks
	c := b.tx.handle()
	*c = Bucket{tx: b.tx, data: nd, parent: b, name: k, seen: b.tx.relinks}
	return c
}

func (b *Bucket) CreateBucketIfNotExists(key []byte) (*Bucket, error) {
	if err := b.checkWrite(); err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, errors.ErrBucketNameRequired
	}
	if n := get(b.cur().root, key); n != nil {
		if n.bkt == nil {
			return nil, errors.ErrIncompatibleValue
		}
		return b.open(n), nil
	}
	return b.create(key), nil
}

func (b *Bucket) DeleteBucket(key []byte) error {
	if err := b.checkWrite(); err != nil {
		return err
	}
	n := get(b.cur().root, key)
	if n == nil {
		return errors.ErrBucketNotFound
	}
	if n.bkt == nil {
		return errors.ErrIncompatibleValue
	}
	d := b.mutable()
	d.root = remove(d.root, key, b.tx.gen)
	b.tx.relinks++
	b.seen = b.tx.relinks
	return nil
}

func (b *Bucket) Get(key []byte) []byte {
	n := get(b.cur().root, key)
	if n == nil || n.bkt != nil {
		return nil
	}
	return n.val()
}

func (b *Bucket) Put(key []byte, value []byte) error {
	if err := b.checkWrite(); err != nil {
		return err
	}
	if len(key) == 0 {
		return errors.ErrKeyRequired
	}
	if len(key) > MaxKeySize {
		return errors.ErrKeyTooLarge
	}
	if int64(len(value)) > MaxValueSize {
		return errors.ErrValueTooLarge
	}
	n := get(b.cur().root, key)
	if n != nil && n.bkt != nil {
		return errors.ErrIncompatibleValue
	}
	d := b.mutable()
	kv := make([]byte, len(key)+len(value))
	copy(kv, key)
	copy(kv[len(key):], value)
	var p uint32
	if n != nil {
		p = n.prio
	} else {
		p = prio(key)
	}
	d.root = upsert(d.root, kv[:len(key)], kv, uint32(len(key)), nil, p, b.tx.gen)
	return nil
}

func (b *Bucket) Delete(key []byte) error {
	if err := b.checkWrite(); err != nil {
		return err
	}
	n := get(b.cur().root, key)
	if n == nil {
		return nil
	}
	if n.bkt != nil {
		return errors.ErrIncompatibleValue
	}
	d := b.mutable()
	d.root = remove(d.root, key, b.tx.gen)
	return nil
}

func (b *Bucket) Sequence() uint64 { return b.cur().seq }

func (b *Bucket) SetSequence(v uint64) error {
	if err := b.checkWrite(); err != nil {
		return err
	}
	b.mutable().seq = v
	return nil
}

func (b *Bucket) NextSequence() (uint64, error) {
	if err := b.checkWrite(); err != nil {
		return 0, err
	}
	d := b.mutable()
	d.seq++
	return d.seq, nil
}

// ForEach calls fn for every key in the bucket as of the call; a nested
// bucket's value is nil. As in bbolt, fn must not change the bucket.
func (b *Bucket) ForEach(fn func(k, v []byte) error) error {
	if b.tx != nil && b.tx.done {
		return errors.ErrTxClosed
	}
	return walk(b.cur().root, func(n *node) error {
		if n.bkt != nil {
			return fn(n.key(), nil)
		}
		return fn(n.key(), n.val())
	})
}

func (b *Bucket) ForEachBucket(fn func(k []byte) error) error {
	if b.tx != nil && b.tx.done {
		return errors.ErrTxClosed
	}
	return walk(b.cur().root, func(n *node) error {
		if n.bkt == nil {
			return nil
		}
		return fn(n.key())
	})
}

func (b *Bucket) Cursor() *Cursor { return &Cursor{bucket: b} }

// Cursor iterates a bucket in key order. It remembers its position by key, so
// changes to the bucket between moves are seen as they would be in bbolt.
type Cursor struct {
	bucket *Bucket
	cur    *node
}

func (c *Cursor) Bucket() *Bucket { return c.bucket }

func (c *Cursor) at(n *node) ([]byte, []byte) {
	c.cur = n
	if n == nil {
		return nil, nil
	}
	if n.bkt != nil {
		return n.key(), nil
	}
	return n.key(), n.val()
}

func (c *Cursor) First() ([]byte, []byte) { return c.at(first(c.bucket.cur().root)) }
func (c *Cursor) Last() ([]byte, []byte)  { return c.at(last(c.bucket.cur().root)) }
func (c *Cursor) Seek(seek []byte) ([]byte, []byte) {
	return c.at(ceil(c.bucket.cur().root, seek, false))
}

func (c *Cursor) Next() ([]byte, []byte) {
	if c.cur == nil {
		return nil, nil
	}
	return c.at(ceil(c.bucket.cur().root, c.cur.key(), true))
}

func (c *Cursor) Prev() ([]byte, []byte) {
	if c.cur == nil {
		return nil, nil
	}
	return c.at(before(c.bucket.cur().root, c.cur.key()))
}

func (c *Cursor) Delete() error {
	if err := c.bucket.checkWrite(); err != nil {
		return err
	}
	if c.cur == nil {
		return nil
	}
	if c.cur.bkt != nil {
		return errors.ErrIncompatibleValue
	}
	return c.bucket.Delete(c.cur.key())
}

// BucketStats carries only KeyN, the number of keys directly in the bucket.
type BucketStats struct {
	KeyN int
}

func (b *Bucket) Stats() BucketStats {
	var s BucketStats
	_ = walk(b.cur().root, func(*node) error { s.KeyN++; return nil })
	return s
}
