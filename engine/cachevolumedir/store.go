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
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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

	// mu guards pins and the moves GC makes, so that a volume is never moved
	// while pinned and never pinned while half-moved.
	mu   sync.Mutex
	pins map[string]*pinState
	// pendingBytes are the bytes of volumes moved to trash whose removal
	// hasn't finished.
	pendingBytes int64
	removals     sync.WaitGroup

	// beforeRemove, when set, runs before each background removal; tests
	// use it to hold removals.
	beforeRemove func()
	// removeAll removes a tree; os.RemoveAll, replaced by tests.
	removeAll func(string) error
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
		dir:       dir,
		formDir:   filepath.Join(dir, "v"+strconv.Itoa(FormatVersion)),
		opts:      opts,
		lock:      lock,
		pins:      map[string]*pinState{},
		removeAll: os.RemoveAll,
	}
	for _, sub := range []string{"volumes", "tmp", "trash"} {
		if err := os.MkdirAll(filepath.Join(s.formDir, sub), 0o755); err != nil {
			s.Close()
			return nil, fmt.Errorf("create cache volume directory layout: %w", err)
		}
	}
	// This process owns the directory now, so anything in tmp/ or trash/ was
	// left by a previous owner that stopped mid-way.
	for _, sub := range []string{"tmp", "trash"} {
		if err := removeContents(filepath.Join(s.formDir, sub)); err != nil {
			s.Close()
			return nil, fmt.Errorf("remove leftover cache volume %s: %w", sub, err)
		}
	}
	return s, nil
}

// Dir is the directory the store owns.
func (s *Store) Dir() string {
	return s.dir
}

// ErrPinned is returned by Close when volumes are still pinned.
var ErrPinned = errors.New("cache volumes are still in use")

// abandon releases ownership without checking pins, as a crashed process
// would; for tests.
func (s *Store) abandon() {
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
}

// Close gives up ownership. Pins still held lose their protection. Removals in
// progress keep running until they finish.
func (s *Store) Close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// Pin keeps a volume from being collected until it is released.
type Pin struct {
	s        *Store
	id       string
	path     string
	writable bool
	once     sync.Once
}

// Path is the volume's contents directory, to bind-mount.
func (p *Pin) Path() string {
	return p.path
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

	unlock := s.createMu.lock(id)
	defer unlock()

	// Holding createMu excludes creating this volume elsewhere; s.mu excludes
	// removing it, and a pinned volume is never removed. A removal can still
	// land between the check and the pin, so check again under s.mu.
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
	if st == nil {
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

	pin := &Pin{s: s, id: id, path: filepath.Join(volDir, fsName), writable: writable}
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
	ID       string
	Identity Identity
	// Format is the volume's on-disk format; volumes of another format have
	// no identity.
	Format     int
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
	entries, err := s.scan(ctx)
	if err != nil {
		return nil, 0, err
	}
	used := int64(0)
	for _, e := range entries {
		used += e.SizeBytes
	}
	// Volumes being created count at their current size.
	tmpBytes, err := treeSize(ctx, filepath.Join(s.formDir, "tmp"))
	if err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	pending := s.pendingBytes
	s.mu.Unlock()
	return entries, used + tmpBytes + pending, nil
}

// scan lists every volume of every format, measuring stale sizes.
func (s *Store) scan(ctx context.Context) ([]Entry, error) {
	var entries []Entry
	forms, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read cache volume directory: %w", err)
	}
	for _, form := range forms {
		format, ok := formatOf(form)
		if !ok {
			continue
		}
		volsDir := filepath.Join(s.dir, form.Name(), "volumes")
		vols, err := os.ReadDir(volsDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read cache volumes: %w", err)
		}
		for _, vol := range vols {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !vol.IsDir() {
				continue
			}
			var e Entry
			var err error
			if format == FormatVersion {
				e, err = s.currentEntry(ctx, vol.Name())
			} else {
				e, err = otherFormatEntry(ctx, filepath.Join(volsDir, vol.Name()), format)
			}
			if errors.Is(err, fs.ErrNotExist) {
				// Removed while scanning.
				continue
			}
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func (s *Store) currentEntry(ctx context.Context, id string) (Entry, error) {
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
		Format:     FormatVersion,
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

	size, err := treeSize(ctx, filepath.Join(volDir, fsName))
	if err != nil {
		return Entry{}, err
	}
	e.SizeBytes = size

	s.mu.Lock()
	settled := writersBefore == 0
	if st := s.pins[id]; st != nil {
		settled = settled && st.writers == 0 && st.writeEpoch == epoch
	}
	s.mu.Unlock()
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

func otherFormatEntry(ctx context.Context, path string, format int) (Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}
	size, err := treeSize(ctx, path)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		ID:         filepath.Base(path),
		Format:     format,
		SizeBytes:  size,
		LastUsedAt: info.ModTime(),
		path:       path,
	}, nil
}

// Policy decides what Collect removes. Byte values are resolved; zero means
// unset. Thresholds work as in the engine's other cache policies.
type Policy struct {
	ReservedSpace int64
	MaxUsedSpace  int64
	MinFreeSpace  int64
	TargetSpace   int64
	// CurrentFreeSpace is the filesystem's free bytes, for MinFreeSpace.
	CurrentFreeSpace int64
	// KeepDuration keeps volumes used more recently than this.
	KeepDuration time.Duration
	// All removes every volume that isn't pinned, whatever the thresholds
	// and KeepDuration.
	All bool
}

// targetBytes is how many bytes the policy wants freed from usedBytes.
func (p Policy) targetBytes(usedBytes int64) int64 {
	if p.All {
		return math.MaxInt64
	}
	target := int64(0)
	triggered := false
	if p.MaxUsedSpace > 0 && usedBytes > p.MaxUsedSpace {
		triggered = true
		target = max(target, usedBytes-max(p.MaxUsedSpace, p.ReservedSpace))
	}
	if p.MinFreeSpace > 0 && p.CurrentFreeSpace < p.MinFreeSpace {
		triggered = true
		target = max(target, min(p.MinFreeSpace-p.CurrentFreeSpace, usedBytes-p.ReservedSpace))
	}
	if triggered && p.TargetSpace > 0 && usedBytes > p.TargetSpace {
		target = max(target, usedBytes-max(p.TargetSpace, p.ReservedSpace))
	}
	return max(target, 0)
}

// Report is what one Collect removed.
type Report struct {
	Removed        []Entry
	ReclaimedBytes int64
}

// Collect removes volumes until the policy is met: volumes of another format
// first, then the least recently used, never a pinned one. Bytes already being
// removed count as freed. Removed volumes are moved aside at once and deleted
// in the background. Collect stops between volumes when ctx ends, leaving the
// directory consistent.
func (s *Store) Collect(ctx context.Context, policy Policy) (Report, error) {
	var report Report
	if err := ctx.Err(); err != nil {
		return report, err
	}
	entries, used, err := s.Usage(ctx)
	if err != nil {
		return report, err
	}
	if s.afterUsage != nil {
		s.afterUsage()
	}
	s.mu.Lock()
	pending := s.pendingBytes
	s.mu.Unlock()
	need := policy.targetBytes(used)
	if !policy.All {
		// What is already being removed counts as freed.
		need -= pending
	}
	if need <= 0 {
		return report, nil
	}

	slices.SortStableFunc(entries, func(a, b Entry) int {
		aOther, bOther := a.Format != FormatVersion, b.Format != FormatVersion
		switch {
		case aOther && !bOther:
			return -1
		case !aOther && bOther:
			return 1
		}
		return a.LastUsedAt.Compare(b.LastUsedAt)
	})
	now := s.opts.Now()
	for _, e := range entries {
		if report.ReclaimedBytes >= need {
			break
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !policy.All && policy.KeepDuration > 0 && e.Format == FormatVersion && now.Sub(e.LastUsedAt) < policy.KeepDuration {
			continue
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
	if e.Format == FormatVersion {
		if st := s.pins[e.ID]; st != nil && st.count > 0 {
			s.mu.Unlock()
			os.Remove(trash)
			return false, nil
		}
	}
	if err := os.Rename(e.path, target); err != nil {
		s.mu.Unlock()
		os.Remove(trash)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("remove cache volume %s: %w", e.ID, err)
	}
	if e.Format == FormatVersion {
		delete(s.pins, e.ID)
	}
	s.pendingBytes += e.SizeBytes
	s.removals.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.removals.Done()
		if s.beforeRemove != nil {
			s.beforeRemove()
		}
		_ = s.removeAll(trash)
		s.mu.Lock()
		s.pendingBytes -= e.SizeBytes
		s.mu.Unlock()
	}()
	return true, nil
}

// WaitRemovals waits until every removal Collect started has finished, or ctx
// ends.
func (s *Store) WaitRemovals(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.removals.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
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

func formatOf(ent fs.DirEntry) (int, bool) {
	if !ent.IsDir() {
		return 0, false
	}
	n, ok := strings.CutPrefix(ent.Name(), "v")
	if !ok {
		return 0, false
	}
	format, err := strconv.Atoi(n)
	if err != nil || format <= 0 || strconv.Itoa(format) != n {
		return 0, false
	}
	return format, true
}

// treeSize is the disk space a tree takes, counting each inode once.
func treeSize(ctx context.Context, root string) (int64, error) {
	type inode struct{ dev, ino uint64 }
	seen := map[inode]struct{}{}
	var size int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Changed while walking.
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			size += info.Size()
			return nil
		}
		key := inode{uint64(st.Dev), uint64(st.Ino)} //nolint:unconvert // types differ by platform
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		size += int64(st.Blocks) * 512 //nolint:unconvert // types differ by platform
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return size, err
}

func removeContents(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if err := os.RemoveAll(filepath.Join(dir, ent.Name())); err != nil {
			return err
		}
	}
	return nil
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
