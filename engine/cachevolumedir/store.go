// Package cachevolumedir keeps cache volumes as plain directories under one
// directory, apart from the engine's snapshots, so that the directory can be
// handed from one engine to the next.
//
// One engine owns the directory at a time. Every volume is named by a digest of
// its identity and carries its own metadata, so an engine that receives the
// directory from another can use it with no database.
//
// Layout, for format 1:
//
//	<dir>/owner.lock             flock held by the owning process
//	<dir>/v1/volumes/<id>/       one volume
//	    meta.json                identity and bookkeeping (temp file + rename)
//	    dirty                    present while the stored size may be stale
//	    fs/                      the volume's contents
//	<dir>/v1/tmp/                volumes being created
//	<dir>/v1/trash/              volumes being removed
//
// The store reads and writes only its own format's subdirectory, and leaves
// anything else under <dir> alone.
//
// Every change a crash could interrupt is a rename or a temp-file-and-rename,
// so the directory is consistent at any point a process stops.
package cachevolumedir

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

// FormatVersion is the on-disk format this package writes, as v<N>/ under the
// directory.
const FormatVersion = 1

// ErrOwned is returned by Open when another process owns the directory.
var ErrOwned = errors.New("cache volume directory is owned by another process")

const (
	ownerLockName = "owner.lock"
	metaName      = "meta.json"
	dirtyName     = "dirty"
	fsName        = "fs"

	// lastUsedWriteInterval throttles rewriting meta.json on every use.
	lastUsedWriteInterval = time.Minute
)

// Identity is what names a cache volume. Two calls with equal identities use
// the same directory, in any engine.
type Identity struct {
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key"`
	Sharing   string `json:"sharing,omitempty"`
	Owner     string `json:"owner,omitempty"`
	// Source is the recipe digest of the volume's source directory, or empty.
	// The recipe includes which subdirectory of its parent the source is, so
	// the identity needs no separate selector.
	Source string `json:"source,omitempty"`
}

// ID is the volume's directory name: the hex SHA-256 of the identity's
// canonical JSON, versioned.
func (id Identity) ID() string {
	// A fixed field order, every field present, and JSON string escaping
	// keep the encoding canonical.
	payload, err := json.Marshal(struct {
		V         int    `json:"v"`
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Sharing   string `json:"sharing"`
		Owner     string `json:"owner"`
		Source    string `json:"source"`
	}{FormatVersion, id.Namespace, id.Key, id.Sharing, id.Owner, id.Source})
	if err != nil {
		// Marshaling a struct of strings can't fail.
		panic(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Options configures a Store.
type Options struct {
	// EngineVersion is recorded in the metadata of volumes this store creates.
	EngineVersion string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// RemoveAll deletes a removed volume's tree; os.RemoveAll when nil.
	// Tests replace it to hold a deletion.
	RemoveAll func(string) error
	// OnDeletionError, when set, is told of each background deletion that
	// failed, as it fails: nothing waits for the deletions to report them.
	OnDeletionError func(error)
	// OnLeftovers, when set, is told what a previous owner left when Open
	// finds it: how many trees, the bytes their volumes recorded, and how
	// many recorded no size and are measured as they're deleted.
	OnLeftovers func(count int, recordedBytes int64, unmeasured int)
	// OnLeftoversDeleted, when set, is told when the background deletion of
	// a previous owner's leftovers has ended, and how long it took.
	OnLeftoversDeleted func(count int, took time.Duration)
}

// Store is an owned cache volume directory.
type Store struct {
	dir     string
	formDir string
	opts    Options
	lock    *os.File

	// createMu serializes creating each volume.
	createMu keyedMutex
	// metaMu serializes read-modify-writes of meta.json files.
	metaMu sync.Mutex
	// collecting holds a token while a Collect runs: each decides from the
	// usage it reads, which another's removals would change under it. A
	// Collect waiting for the token gives up when its context ends.
	collecting chan struct{}

	// mu guards pins and the moves GC makes, so that a volume is never moved
	// while pinned and never pinned while half-moved.
	mu   sync.Mutex
	pins map[string]*pinState
	// acquiring counts Acquire calls in progress, which Close waits out like
	// pins: they may be creating a volume before they hold a pin.
	acquiring int
	// stuckBytes are the bytes a failed removal left in trash; they take
	// space until a later owner's open removes them.
	stuckBytes int64
	// pendingBytes are the bytes of volumes moved to trash whose removal
	// hasn't finished.
	pendingBytes int64
	removals     sync.WaitGroup
	// removalErr collects background removal failures until WaitRemovals
	// reports them.
	removalErr error

	// beforeRemove, when set, runs before each background removal; tests
	// use it to hold removals.
	beforeRemove func()
	// removeAll removes a tree; os.RemoveAll, replaced by tests.
	removeAll func(string) error
	// trashSize measures what a failed removal left; treeSize, replaced
	// by tests.
	trashSize func(context.Context, string) (int64, error)
	// afterUsage and beforeClearDirty, when set, run at those points of
	// Collect and of a measurement; tests use them to interleave.
	afterUsage       func()
	beforeClearDirty func()
}

type pinState struct {
	count   int
	writers int
	// writeEpoch counts writable pins taken, so a measurement can tell
	// whether a writer came and went while it walked.
	writeEpoch uint64
	// lastUsedSet is when this process last wrote the volume's lastUsedAt.
	lastUsedSet time.Time
}

// Open takes ownership of dir, creating it and its layout if needed, and
// removes what a previous owner left in tmp/ and trash/. It returns ErrOwned
// if another process owns dir.
func Open(dir string, opts Options) (*Store, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RemoveAll == nil {
		opts.RemoveAll = os.RemoveAll
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache volume directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ownerLockName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open cache volume directory lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s: %w", dir, ErrOwned)
		}
		return nil, fmt.Errorf("lock cache volume directory: %w", err)
	}
	s := &Store{
		dir:        dir,
		formDir:    filepath.Join(dir, "v"+strconv.Itoa(FormatVersion)),
		opts:       opts,
		lock:       lock,
		pins:       map[string]*pinState{},
		removeAll:  opts.RemoveAll,
		trashSize:  treeSize,
		collecting: make(chan struct{}, 1),
	}
	for _, sub := range []string{"volumes", "tmp", "trash"} {
		if err := os.MkdirAll(filepath.Join(s.formDir, sub), 0o755); err != nil {
			s.Close()
			return nil, fmt.Errorf("create cache volume directory layout: %w", err)
		}
	}
	// This process owns the directory now, so anything in tmp/ or trash/ was
	// left by a previous owner that stopped mid-way: volumes it was creating,
	// and deletions it didn't wait for. They're deleted in the background,
	// counted as space being freed until they're gone, so the engine doesn't
	// wait for them before it serves anything.
	if err := s.deleteLeftovers(); err != nil {
		s.Close()
		return nil, fmt.Errorf("delete leftover cache volumes: %w", err)
	}
	return s, nil
}

// Dir is the directory the store owns.
func (s *Store) Dir() string {
	return s.dir
}

// ErrPinned is returned by Close when volumes are still pinned.
var ErrPinned = errors.New("cache volumes are still in use")

// ErrClosed is returned by Acquire once the store is closed.
var ErrClosed = errors.New("cache volume directory is closed")

// Close gives up ownership. If volumes are still pinned, or acquisitions are
// still in progress, it keeps ownership and returns ErrPinned: another owner
// could otherwise collect a volume that is still in use, or sweep one being
// created. The ownership lock then lasts until the process exits. Once
// closed, Acquire fails with ErrClosed. Deletions in progress keep running
// until they finish or the process exits; the next owner deletes what's left.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	pinned := 0
	for _, st := range s.pins {
		pinned += st.count
	}
	if pinned > 0 || s.acquiring > 0 {
		return fmt.Errorf("keeping ownership of %s: %d pins, %d acquisitions in progress: %w", s.dir, pinned, s.acquiring, ErrPinned)
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// Closed reports whether the store has given up ownership. A nil store is
// closed.
func (s *Store) Closed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lock == nil
}

// abandon releases ownership without checking pins, as a crashed process
// would; for tests.
func (s *Store) abandon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
}

// Pin keeps a volume from being collected until it is released.
type Pin struct {
	s        *Store
	id       string
	path     string
	writable bool
	created  bool
	first    bool
	once     sync.Once
}

// Path is the volume's contents directory, to bind-mount.
func (p *Pin) Path() string {
	return p.path
}

// Created reports whether the Acquire that took this pin created the volume.
func (p *Pin) Created() bool {
	return p.created
}

// FirstPin reports whether this is the store's first pin on the volume since
// it took the directory, or since it last removed the volume.
func (p *Pin) FirstPin() bool {
	return p.first
}

// Release ends the pin. Only the first call counts.
func (p *Pin) Release() {
	p.once.Do(func() {
		p.s.mu.Lock()
		defer p.s.mu.Unlock()
		st := p.s.pins[p.id]
		if st == nil {
			return
		}
		st.count--
		if p.writable {
			st.writers--
		}
	})
}

// Acquire pins the volume, creating it first if it doesn't exist: populate
// fills the new volume's contents directory, which exists and is empty when it
// is called. Creation of one volume is serialized; a failed populate leaves no
// volume behind. A writable pin marks the volume's stored size stale until a
// measurement taken with no writer finds it again.
func (s *Store) Acquire(ctx context.Context, ident Identity, writable bool, populate func(ctx context.Context, fsDir string) error) (*Pin, error) {
	id := ident.ID()
	volDir := s.volumeDir(id)

	// Counted from before any filesystem work, so Close can't give the
	// directory away under a volume being created.
	s.mu.Lock()
	if s.lock == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("acquire cache volume %q: %w", ident.Key, ErrClosed)
	}
	s.acquiring++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.acquiring--
		s.mu.Unlock()
	}()

	unlock := s.createMu.lock(id)
	defer unlock()

	// Holding createMu excludes creating this volume elsewhere; s.mu excludes
	// removing it, and a pinned volume is never removed. A removal can still
	// land between the check and the pin, so check again under s.mu.
	var created bool
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		exists, err := pathExists(filepath.Join(volDir, fsName))
		if err != nil {
			return nil, fmt.Errorf("stat cache volume %s: %w", id, err)
		}
		if !exists {
			if err := s.create(ctx, ident, populate); err != nil {
				return nil, err
			}
			created = true
		}
		s.mu.Lock()
		exists, err = pathExists(filepath.Join(volDir, fsName))
		if err == nil && exists {
			break // pin with s.mu held
		}
		s.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("stat cache volume %s: %w", id, err)
		}
	}
	st := s.pins[id]
	first := st == nil
	if first {
		st = &pinState{}
		s.pins[id] = st
	}
	st.count++
	if writable {
		st.writers++
		st.writeEpoch++
	}
	now := s.opts.Now()
	touch := st.lastUsedSet.IsZero() || now.Sub(st.lastUsedSet) >= lastUsedWriteInterval
	if touch {
		st.lastUsedSet = now
	}
	s.mu.Unlock()

	pin := &Pin{s: s, id: id, path: filepath.Join(volDir, fsName), writable: writable, created: created, first: first}
	if writable {
		// Before the writer can write: a crash from here on leaves the
		// marker, and the next owner remeasures.
		if err := writeMarker(filepath.Join(volDir, dirtyName)); err != nil {
			pin.Release()
			return nil, fmt.Errorf("mark cache volume %s dirty: %w", id, err)
		}
	}
	if touch {
		if err := s.updateMeta(id, func(m *meta) { m.LastUsedAt = now }); err != nil {
			pin.Release()
			return nil, err
		}
	}
	return pin, nil
}

func (s *Store) create(ctx context.Context, ident Identity, populate func(ctx context.Context, fsDir string) error) (rerr error) {
	id := ident.ID()
	tmp, err := os.MkdirTemp(filepath.Join(s.formDir, "tmp"), id+"-")
	if err != nil {
		return fmt.Errorf("create cache volume %s: %w", id, err)
	}
	defer func() {
		if rerr != nil {
			os.RemoveAll(tmp)
		}
	}()
	// MkdirTemp makes 0700; a volume's root starts as an ordinary directory,
	// and populate may change it.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("create cache volume %s: %w", id, err)
	}
	fsDir := filepath.Join(tmp, fsName)
	if err := os.Mkdir(fsDir, 0o755); err != nil {
		return fmt.Errorf("create cache volume %s: %w", id, err)
	}
	if err := os.Chmod(fsDir, 0o755); err != nil {
		return fmt.Errorf("create cache volume %s: %w", id, err)
	}
	if populate != nil {
		if err := populate(ctx, fsDir); err != nil {
			return fmt.Errorf("populate cache volume %q: %w", ident.Key, err)
		}
	}
	now := s.opts.Now()
	if err := writeJSON(filepath.Join(tmp, metaName), meta{
		Format:     FormatVersion,
		ID:         id,
		Identity:   ident,
		CreatedAt:  now,
		CreatedBy:  s.opts.EngineVersion,
		LastUsedAt: now,
	}); err != nil {
		return fmt.Errorf("write cache volume %s metadata: %w", id, err)
	}
	// A new volume has no stored size yet.
	if err := writeMarker(filepath.Join(tmp, dirtyName)); err != nil {
		return fmt.Errorf("mark cache volume %s dirty: %w", id, err)
	}
	if err := os.Rename(tmp, s.volumeDir(id)); err != nil {
		return fmt.Errorf("publish cache volume %s: %w", id, err)
	}
	return nil
}

// PinnedPath is the contents directory of a volume this process has pinned.
// Mounting it is safe while the pin is held; it is an error to ask for a
// volume that isn't pinned, since nothing then keeps it from being removed.
func (s *Store) PinnedPath(ident Identity) (string, error) {
	id := ident.ID()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.pins[id]; st == nil || st.count == 0 {
		return "", fmt.Errorf("cache volume %q (%s) is not pinned", ident.Key, id)
	}
	return filepath.Join(s.volumeDir(id), fsName), nil
}

// Entry describes one volume in the directory.
type Entry struct {
	ID         string
	Identity   Identity
	SizeBytes  int64
	CreatedAt  time.Time
	LastUsedAt time.Time
	Pinned     bool

	// path is the volume's directory.
	path string
}

// Usage lists the volumes, measuring those whose stored size is stale, and
// returns the bytes the whole directory uses, including what is still being
// created or removed.
func (s *Store) Usage(ctx context.Context) ([]Entry, int64, error) {
	entries, used, _, err := s.usage(ctx, nil)
	return entries, used, err
}

// measurement is what a scan spent measuring stale volumes.
type measurement struct {
	volumes int
	bytes   int64
	files   int64
	took    time.Duration
}

func (m *measurement) add(bytes, files int64, took time.Duration) {
	if m == nil {
		return
	}
	m.volumes++
	m.bytes += bytes
	m.files += files
	m.took += took
}

// usage is Usage, also returning the pending-removal bytes it counted. It
// adds what it spent measuring stale volumes to measured, if set.
func (s *Store) usage(ctx context.Context, measured *measurement) ([]Entry, int64, int64, error) {
	entries, err := s.scan(ctx, measured)
	if err != nil {
		return nil, 0, 0, err
	}
	used := int64(0)
	for _, e := range entries {
		used += e.SizeBytes
	}
	// Volumes being created count at their current size.
	tmpBytes, err := treeSize(ctx, filepath.Join(s.formDir, "tmp"))
	if err != nil {
		return nil, 0, 0, err
	}
	s.mu.Lock()
	pending, stuck := s.pendingBytes, s.stuckBytes
	s.mu.Unlock()
	// Stuck trash takes space, but no removal will free it: only pending
	// bytes count as being freed.
	return entries, used + tmpBytes + pending + stuck, pending, nil
}

// scan lists every volume, measuring stale sizes, and adds what that took
// to measured, if set.
func (s *Store) scan(ctx context.Context, measured *measurement) ([]Entry, error) {
	vols, err := os.ReadDir(filepath.Join(s.formDir, "volumes"))
	if err != nil {
		return nil, fmt.Errorf("read cache volumes: %w", err)
	}
	var entries []Entry
	for _, vol := range vols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !vol.IsDir() {
			continue
		}
		e, err := s.volumeEntry(ctx, vol.Name(), measured)
		if errors.Is(err, fs.ErrNotExist) {
			// Removed while scanning.
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *Store) volumeEntry(ctx context.Context, id string, measured *measurement) (Entry, error) {
	volDir := s.volumeDir(id)
	if _, err := os.Stat(filepath.Join(volDir, fsName)); err != nil {
		return Entry{}, err
	}
	m, err := readMeta(filepath.Join(volDir, metaName))
	if err != nil {
		// An unreadable meta.json means "no stored size"; the identity is
		// lost, but the volume can still be measured and collected.
		m = meta{Format: FormatVersion, ID: id}
	}
	e := Entry{
		ID:         id,
		Identity:   m.Identity,
		CreatedAt:  m.CreatedAt,
		LastUsedAt: m.LastUsedAt,
		path:       volDir,
	}
	s.mu.Lock()
	st := s.pins[id]
	e.Pinned = st != nil && st.count > 0
	s.mu.Unlock()

	_, dirtyErr := os.Stat(filepath.Join(volDir, dirtyName))
	stale := dirtyErr == nil || m.SizeBytes == nil
	if !stale {
		e.SizeBytes = *m.SizeBytes
		return e, nil
	}

	s.mu.Lock()
	var epoch uint64
	writersBefore := 0
	if st := s.pins[id]; st != nil {
		epoch, writersBefore = st.writeEpoch, st.writers
	}
	s.mu.Unlock()

	start := time.Now()
	size, files, err := treeWalk(ctx, filepath.Join(volDir, fsName))
	measured.add(size, files, time.Since(start))
	if err != nil {
		return Entry{}, err
	}
	e.SizeBytes = size

	// The final check, the size and the marker's removal happen under s.mu,
	// so a writer can't pin between them: Acquire counts its writer under
	// s.mu before writing the marker, so either this sees it and keeps the
	// marker, or the writer's marker comes after the removal.
	s.mu.Lock()
	defer s.mu.Unlock()
	settled := writersBefore == 0
	if st := s.pins[id]; st != nil {
		settled = settled && st.writers == 0 && st.writeEpoch == epoch
	}
	if !settled {
		// A writer held the volume at some point during the walk: use the
		// size for now, and keep the marker.
		return e, nil
	}
	if err := s.updateMeta(id, func(m *meta) { m.SizeBytes = &size }); err != nil {
		return Entry{}, err
	}
	// Meta first: a crash between the two leaves the marker, which only costs
	// another walk.
	if s.beforeClearDirty != nil {
		s.beforeClearDirty()
	}
	if err := os.Remove(filepath.Join(volDir, dirtyName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("clear cache volume %s dirty marker: %w", id, err)
	}
	return e, nil
}

// Policy decides what Collect removes. Byte values are resolved; zero means
// unset. Thresholds work as in the engine's other cache policies.
type Policy struct {
	ReservedSpace int64
	MaxUsedSpace  int64
	MinFreeSpace  int64
	// CurrentFreeSpace is the filesystem's free bytes, for MinFreeSpace.
	CurrentFreeSpace int64
	// All removes every volume that isn't pinned, whatever the thresholds.
	All bool
}

// targetBytes is how many bytes the policy wants freed from usedBytes.
func (p Policy) targetBytes(usedBytes int64) int64 {
	if p.All {
		return math.MaxInt64
	}
	target := int64(0)
	if p.MaxUsedSpace > 0 && usedBytes > p.MaxUsedSpace {
		target = max(target, usedBytes-max(p.MaxUsedSpace, p.ReservedSpace))
	}
	if p.MinFreeSpace > 0 && p.CurrentFreeSpace < p.MinFreeSpace {
		// The free-space minimum outranks the reserve.
		target = max(target, p.MinFreeSpace-p.CurrentFreeSpace)
	}
	return max(target, 0)
}

// Report is what one Collect removed, and what it measured to decide.
type Report struct {
	Removed        []Entry
	ReclaimedBytes int64
	// UsedBytes is the directory's usage the collection decided on.
	UsedBytes int64

	// Measured is how many volumes written since their last measurement
	// were walked to learn their size, a walk cut off by the context
	// included; MeasuredBytes and MeasuredFiles are what those walks found,
	// and MeasureTime how long they took.
	Measured      int
	MeasuredBytes int64
	MeasuredFiles int64
	MeasureTime   time.Duration
}

// Collect removes volumes until the policy is met, the least recently used
// first, never a pinned one. Bytes already being removed count as freed.
// Removed volumes are moved aside at once and deleted in the background.
// Collect stops between volumes when ctx ends, leaving the directory
// consistent. Collections run one at a time; one waiting for another stops
// waiting when ctx ends.
func (s *Store) Collect(ctx context.Context, policy Policy) (Report, error) {
	var report Report
	select {
	case s.collecting <- struct{}{}:
		defer func() { <-s.collecting }()
	case <-ctx.Done():
		return report, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	var m measurement
	entries, used, pending, err := s.usage(ctx, &m)
	report.Measured, report.MeasuredBytes, report.MeasuredFiles, report.MeasureTime = m.volumes, m.bytes, m.files, m.took
	if err != nil {
		return report, err
	}
	report.UsedBytes = used
	if s.afterUsage != nil {
		s.afterUsage()
	}
	need := policy.targetBytes(used)
	if !policy.All {
		// What is already being removed counts as freed.
		need -= pending
	}
	if need <= 0 {
		return report, nil
	}

	slices.SortStableFunc(entries, func(a, b Entry) int {
		return a.LastUsedAt.Compare(b.LastUsedAt)
	})
	for _, e := range entries {
		if report.ReclaimedBytes >= need {
			break
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		removed, err := s.remove(e)
		if err != nil {
			return report, err
		}
		if !removed {
			continue
		}
		e.Pinned = false
		report.Removed = append(report.Removed, e)
		report.ReclaimedBytes += e.SizeBytes
	}
	return report, nil
}

// remove moves a volume to trash unless it is pinned, and deletes it in the
// background.
func (s *Store) remove(e Entry) (bool, error) {
	trash, err := os.MkdirTemp(filepath.Join(s.formDir, "trash"), "")
	if err != nil {
		return false, fmt.Errorf("remove cache volume %s: %w", e.ID, err)
	}
	target := filepath.Join(trash, "volume")

	s.mu.Lock()
	if st := s.pins[e.ID]; st != nil && st.count > 0 {
		s.mu.Unlock()
		os.Remove(trash)
		return false, nil
	}
	if err := os.Rename(e.path, target); err != nil {
		s.mu.Unlock()
		os.Remove(trash)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("remove cache volume %s: %w", e.ID, err)
	}
	delete(s.pins, e.ID)
	s.startDeletionLocked(trash, e.SizeBytes, "cache volume "+e.ID)
	s.mu.Unlock()
	return true, nil
}

// startDeletionLocked deletes trash, a tree moved into trash/, in the
// background, counting size as pending until it's gone. A negative size is
// unknown: the deletion measures the tree first. s.mu must be held.
func (s *Store) startDeletionLocked(trash string, size int64, what string) {
	if size > 0 {
		s.pendingBytes += size
	}
	s.removals.Add(1)
	go func() {
		defer s.removals.Done()
		if size < 0 {
			measured, err := s.trashSize(context.Background(), trash)
			size = max(measured, 0)
			if err != nil {
				size = 0
			}
			s.mu.Lock()
			s.pendingBytes += size
			s.mu.Unlock()
		}
		if s.beforeRemove != nil {
			s.beforeRemove()
		}
		err := s.removeAll(trash)
		var survived int64
		if err != nil {
			// What survived still takes space: count it as stuck until a
			// later owner's open deletes it. If it can't be measured,
			// assume nothing was freed.
			measured, measureErr := s.trashSize(context.Background(), trash)
			survived = min(max(measured, 0), size)
			if measureErr != nil {
				survived = size
				err = errors.Join(err, fmt.Errorf("measure what survived: %w", measureErr))
			}
		}
		if err != nil {
			err = fmt.Errorf("remove %s: %w", what, err)
			if s.opts.OnDeletionError != nil {
				s.opts.OnDeletionError(err)
			}
		}
		s.mu.Lock()
		s.pendingBytes -= size
		s.stuckBytes += survived
		if err != nil {
			s.removalErr = errors.Join(s.removalErr, err)
		}
		s.mu.Unlock()
	}()
}

// deleteLeftovers deletes, in the background, what a previous owner left in
// tmp/ and trash/. Volumes it was creating move to trash/ first, so tmp/
// holds only this owner's.
func (s *Store) deleteLeftovers() error {
	tmpDir := filepath.Join(s.formDir, "tmp")
	trashDir := filepath.Join(s.formDir, "trash")
	tmps, err := os.ReadDir(tmpDir)
	if err != nil {
		return err
	}
	for _, ent := range tmps {
		trash, err := os.MkdirTemp(trashDir, "")
		if err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(tmpDir, ent.Name()), filepath.Join(trash, "volume")); err != nil {
			return err
		}
	}
	ents, err := os.ReadDir(trashDir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ent := range ents {
		trash := filepath.Join(trashDir, ent.Name())
		s.startDeletionLocked(trash, leftoverSize(trash), "leftover "+ent.Name())
	}
	return nil
}

// leftoverSize is the recorded size of the volume a leftover in trash/
// holds, if it was measured since it was last written, or -1 if it's
// unknown.
func leftoverSize(trash string) int64 {
	vol := filepath.Join(trash, "volume")
	if _, err := os.Stat(filepath.Join(vol, dirtyName)); err == nil {
		return -1
	}
	m, err := readMeta(filepath.Join(vol, metaName))
	if err != nil || m.SizeBytes == nil {
		return -1
	}
	return *m.SizeBytes
}

// WaitRemovals waits until every deletion in progress has finished, those
// Collect started and a previous owner's leftovers alike, or ctx ends. It
// reports the deletions that failed since it last reported.
func (s *Store) WaitRemovals(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.removals.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		err := s.removalErr
		s.removalErr = nil
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) volumeDir(id string) string {
	return filepath.Join(s.formDir, "volumes", id)
}

type meta struct {
	Format     int       `json:"format"`
	ID         string    `json:"id"`
	Identity   Identity  `json:"identity"`
	CreatedAt  time.Time `json:"createdAt"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	// SizeBytes is the last measured size; nil when never measured.
	SizeBytes *int64 `json:"sizeBytes,omitempty"`
}

func (s *Store) updateMeta(id string, update func(*meta)) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	path := filepath.Join(s.volumeDir(id), metaName)
	m, err := readMeta(path)
	if err != nil {
		m = meta{Format: FormatVersion, ID: id}
	}
	update(&m)
	if err := writeJSON(path, m); err != nil {
		return fmt.Errorf("write cache volume %s metadata: %w", id, err)
	}
	return nil
}

func readMeta(path string) (meta, error) {
	var m meta
	dt, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(dt, &m)
	return m, err
}

// writeJSON writes v to path through a temp file and a rename, so a reader
// sees either the old file or the new one.
func writeJSON(path string, v any) error {
	dt, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + rand.Text()
	if err := os.WriteFile(tmp, dt, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

func writeMarker(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// treeSize is the disk space a tree takes, counting each inode once.
func treeSize(ctx context.Context, root string) (int64, error) {
	size, _, err := treeWalk(ctx, root)
	return size, err
}

// treeWalkers is how many directories a walk reads at once. A walk waits
// on the kernel's directory reads and stats, one entry at a time, and a
// volume such as a Go build cache spreads its entries over many
// directories, so those are read side by side.
const treeWalkers = 16

// treeWalk is treeSize, also returning how many entries it counted.
func treeWalk(ctx context.Context, root string) (int64, int64, error) {
	info, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	w := newTreeWalker()
	w.count(info)
	if !info.IsDir() {
		return w.size.Load(), w.files.Load(), nil
	}
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(treeWalkers)
	eg.Go(func() error { return w.walkDir(ctx, eg, root) })
	err = eg.Wait()
	return w.size.Load(), w.files.Load(), err
}

type treeInode struct{ dev, ino uint64 }

// treeWalkerShards splits the set of counted inodes, so the directories a
// walk reads at once rarely wait on each other to record one.
const treeWalkerShards = 64

// treeWalker is one treeWalk's count, shared by the directories it reads at
// once.
type treeWalker struct {
	size  atomic.Int64
	files atomic.Int64
	// seen holds every inode counted, so a hard link takes no more space,
	// even if its link count changes during the walk.
	seen [treeWalkerShards]struct {
		mu     sync.Mutex
		inodes map[treeInode]struct{}
	}
}

func newTreeWalker() *treeWalker {
	w := &treeWalker{}
	for i := range w.seen {
		w.seen[i].inodes = map[treeInode]struct{}{}
	}
	return w
}

// count adds an entry, and the space it takes unless that inode was
// counted already.
func (w *treeWalker) count(info fs.FileInfo) {
	w.files.Add(1)
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		w.size.Add(info.Size())
		return
	}
	key := treeInode{uint64(st.Dev), uint64(st.Ino)} //nolint:unconvert // types differ by platform
	shard := &w.seen[key.ino%treeWalkerShards]
	shard.mu.Lock()
	_, counted := shard.inodes[key]
	shard.inodes[key] = struct{}{}
	shard.mu.Unlock()
	if counted {
		return
	}
	w.size.Add(int64(st.Blocks) * 512) //nolint:unconvert // types differ by platform
}

// walkDir counts dir's entries, and the trees of its subdirectories: in a
// goroutine of eg's while one is free, here otherwise.
func (w *treeWalker) walkDir(ctx context.Context, eg *errgroup.Group, dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Removed while walking.
			return nil
		}
		return err
	}
	defer f.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ents, readErr := f.ReadDir(1024)
		for _, ent := range ents {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := ent.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return err
			}
			w.count(info)
			if !ent.IsDir() {
				continue
			}
			sub := filepath.Join(dir, ent.Name())
			if !eg.TryGo(func() error { return w.walkDir(ctx, eg, sub) }) {
				if err := w.walkDir(ctx, eg, sub); err != nil {
					return err
				}
			}
		}
		switch {
		case errors.Is(readErr, io.EOF):
			return nil
		case errors.Is(readErr, fs.ErrNotExist):
			return nil
		case readErr != nil:
			return readErr
		}
	}
}

// keyedMutex is a mutex per key.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu    sync.Mutex
	users int
}

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyedMutexEntry{}
	}
	e := k.locks[key]
	if e == nil {
		e = &keyedMutexEntry{}
		k.locks[key] = e
	}
	e.users++
	k.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.users--
		if e.users == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
