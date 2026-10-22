package cachevolumedir

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const mib = 1 << 20

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openStore(t *testing.T, dir string, clock *fakeClock) *Store {
	t.Helper()
	opts := Options{EngineVersion: "test"}
	if clock != nil {
		opts.Now = clock.Now
	}
	s, err := Open(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o644))
}

func emptyPopulate(context.Context, string) error { return nil }

// use acquires the volume, optionally writes a file into it, and releases it.
func use(t *testing.T, s *Store, ident Identity, writable bool, file string, size int) string {
	t.Helper()
	pin, err := s.Acquire(t.Context(), ident, writable, emptyPopulate)
	require.NoError(t, err)
	defer pin.Release()
	if file != "" {
		writeFile(t, filepath.Join(pin.Path(), file), size)
	}
	return pin.Path()
}

func volumeDir(dir string, ident Identity) string {
	return filepath.Join(dir, "v1", "volumes", ident.ID())
}

func entryByKey(entries []Entry, key string) (Entry, bool) {
	for _, e := range entries {
		if e.Identity.Key == key {
			return e, true
		}
	}
	return Entry{}, false
}

func TestIdentityID(t *testing.T) {
	base := Identity{Namespace: "mainClient", Key: "go-build", Sharing: "SHARED", Owner: "1000:1000", Source: "sha256:abc"}
	id := base.ID()
	require.Len(t, id, 64)
	require.Regexp(t, "^[0-9a-f]{64}$", id)
	require.Equal(t, id, base.ID(), "deterministic")

	for name, changed := range map[string]Identity{
		"namespace": {Namespace: "mod(x)", Key: base.Key, Sharing: base.Sharing, Owner: base.Owner, Source: base.Source},
		"key":       {Namespace: base.Namespace, Key: "go-mod", Sharing: base.Sharing, Owner: base.Owner, Source: base.Source},
		"sharing":   {Namespace: base.Namespace, Key: base.Key, Sharing: "LOCKED", Owner: base.Owner, Source: base.Source},
		"owner":     {Namespace: base.Namespace, Key: base.Key, Sharing: base.Sharing, Owner: "0:0", Source: base.Source},
		"source":    {Namespace: base.Namespace, Key: base.Key, Sharing: base.Sharing, Owner: base.Owner, Source: "sha256:def"},
	} {
		require.NotEqual(t, id, changed.ID(), name)
	}
	// Field boundaries matter: moving text between fields changes the ID.
	require.NotEqual(t,
		Identity{Namespace: "ab", Key: "c"}.ID(),
		Identity{Namespace: "a", Key: "bc"}.ID())
}

func TestPinnedPath(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	ident := Identity{Key: "k"}
	_, err := s.PinnedPath(ident)
	require.Error(t, err, "an unpinned volume has no safe path")

	pin, err := s.Acquire(t.Context(), ident, false, emptyPopulate)
	require.NoError(t, err)
	path, err := s.PinnedPath(ident)
	require.NoError(t, err)
	require.Equal(t, pin.Path(), path)

	pin.Release()
	_, err = s.PinnedPath(ident)
	require.Error(t, err)
}

func TestOpenOwnership(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	require.NoError(t, err)

	_, err = Open(dir, Options{})
	require.ErrorIs(t, err, ErrOwned, "a second owner is refused")

	require.NoError(t, s.Close())
	s2, err := Open(dir, Options{})
	require.NoError(t, err, "ownership is free again after Close")
	require.NoError(t, s2.Close())
}

func TestOpenRemovesDebris(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"v1/tmp/half-made/fs", "v1/trash/half-gone/fs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		writeFile(t, filepath.Join(dir, sub, "f"), 10)
	}
	openStore(t, dir, nil)
	for _, sub := range []string{"v1/tmp", "v1/trash"} {
		ents, err := os.ReadDir(filepath.Join(dir, sub))
		require.NoError(t, err)
		require.Empty(t, ents, sub)
	}
}

func TestAcquireCreatesOnceAndReuses(t *testing.T) {
	dir := t.TempDir()
	ident := Identity{Namespace: "mainClient", Key: "k"}
	var populated atomic.Int32
	populate := func(_ context.Context, fsDir string) error {
		populated.Add(1)
		writeFile(t, filepath.Join(fsDir, "seed"), 3)
		return nil
	}

	s := openStore(t, dir, nil)
	pin, err := s.Acquire(t.Context(), ident, true, populate)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(volumeDir(dir, ident), "fs"), pin.Path())
	writeFile(t, filepath.Join(pin.Path(), "written"), 5)
	pin.Release()
	pin.Release() // only the first release counts

	pin, err = s.Acquire(t.Context(), ident, false, populate)
	require.NoError(t, err)
	pin.Release()
	require.EqualValues(t, 1, populated.Load())

	// Another owner of the same directory, later, finds the same volume.
	require.NoError(t, s.Close())
	s2 := openStore(t, dir, nil)
	pin, err = s2.Acquire(t.Context(), ident, false, populate)
	require.NoError(t, err)
	defer pin.Release()
	require.EqualValues(t, 1, populated.Load())
	require.FileExists(t, filepath.Join(pin.Path(), "seed"))
	require.FileExists(t, filepath.Join(pin.Path(), "written"))
}

func TestAcquireConcurrentCreatesOnce(t *testing.T) {
	s := openStore(t, t.TempDir(), nil)
	ident := Identity{Key: "k"}
	var populated atomic.Int32
	populate := func(context.Context, string) error {
		populated.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	pins := make([]*Pin, 8)
	errs := make([]error, 8)
	for i := range pins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pins[i], errs[i] = s.Acquire(t.Context(), ident, true, populate)
		}()
	}
	wg.Wait()
	for i := range pins {
		require.NoError(t, errs[i])
		pins[i].Release()
	}
	require.EqualValues(t, 1, populated.Load())
}

func TestAcquireFailedPopulateLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	ident := Identity{Key: "k"}
	boom := errors.New("boom")
	_, err := s.Acquire(t.Context(), ident, true, func(_ context.Context, fsDir string) error {
		writeFile(t, filepath.Join(fsDir, "partial"), 10)
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.NoDirExists(t, volumeDir(dir, ident))
	ents, err := os.ReadDir(filepath.Join(dir, "v1", "tmp"))
	require.NoError(t, err)
	require.Empty(t, ents)

	pin, err := s.Acquire(t.Context(), ident, true, emptyPopulate)
	require.NoError(t, err, "the next use creates it again")
	defer pin.Release()
	require.NoFileExists(t, filepath.Join(pin.Path(), "partial"))
}

func TestUsageMeasuresWrites(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	ident := Identity{Key: "k"}
	dirty := filepath.Join(volumeDir(dir, ident), "dirty")

	use(t, s, ident, true, "a", mib)
	require.FileExists(t, dirty, "a writable pin marks the size stale")

	entries, used, err := s.Usage(t.Context())
	require.NoError(t, err)
	e, ok := entryByKey(entries, "k")
	require.True(t, ok)
	require.GreaterOrEqual(t, e.SizeBytes, int64(mib))
	require.GreaterOrEqual(t, used, int64(mib))
	require.NoFileExists(t, dirty, "a measurement with no writer clears the marker")

	use(t, s, ident, true, "b", 2*mib)
	entries, _, err = s.Usage(t.Context())
	require.NoError(t, err)
	e, _ = entryByKey(entries, "k")
	require.GreaterOrEqual(t, e.SizeBytes, int64(3*mib))

	use(t, s, ident, false, "", 0)
	require.NoFileExists(t, dirty, "a read-only pin doesn't mark it")
}

func TestUsageDuringWriteKeepsMarker(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	ident := Identity{Key: "k"}
	dirty := filepath.Join(volumeDir(dir, ident), "dirty")

	pin, err := s.Acquire(t.Context(), ident, true, emptyPopulate)
	require.NoError(t, err)
	writeFile(t, filepath.Join(pin.Path(), "a"), mib)
	_, _, err = s.Usage(t.Context())
	require.NoError(t, err)
	require.FileExists(t, dirty, "a measurement taken while a writer holds the volume stays provisional")

	writeFile(t, filepath.Join(pin.Path(), "b"), 2*mib)
	pin.Release()
	entries, _, err := s.Usage(t.Context())
	require.NoError(t, err)
	e, _ := entryByKey(entries, "k")
	require.GreaterOrEqual(t, e.SizeBytes, int64(3*mib), "the writes made during the first measurement are counted")
	require.NoFileExists(t, dirty)
}

func TestSizeSurvivesCrash(t *testing.T) {
	dir := t.TempDir()
	ident := Identity{Key: "k"}

	s, err := Open(dir, Options{})
	require.NoError(t, err)
	use(t, s, ident, true, "small", 1)
	_, _, err = s.Usage(t.Context())
	require.NoError(t, err)

	// A writer pins, writes, and the engine dies before releasing.
	pin, err := s.Acquire(t.Context(), ident, true, emptyPopulate)
	require.NoError(t, err)
	writeFile(t, filepath.Join(pin.Path(), "big"), 2*mib)
	s.abandon()

	s2 := openStore(t, dir, nil)
	entries, _, err := s2.Usage(t.Context())
	require.NoError(t, err)
	e, ok := entryByKey(entries, "k")
	require.True(t, ok)
	require.GreaterOrEqual(t, e.SizeBytes, int64(2*mib), "the next owner remeasures")
}

func TestCollectLRUUnderCap(t *testing.T) {
	clock := newFakeClock()
	s := openStore(t, t.TempDir(), clock)
	for _, key := range []string{"old", "mid", "new"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}

	report, err := s.Collect(t.Context(), Policy{MaxUsedSpace: 2*mib + mib/2})
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)
	require.Equal(t, "old", report.Removed[0].Identity.Key)
	require.GreaterOrEqual(t, report.ReclaimedBytes, int64(mib))
	require.NoError(t, s.WaitRemovals(t.Context()))

	entries, _, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 2)
	_, ok := entryByKey(entries, "old")
	require.False(t, ok)
}

func TestCollectSkipsPinnedAndRecent(t *testing.T) {
	clock := newFakeClock()
	s := openStore(t, t.TempDir(), clock)
	for _, key := range []string{"a", "b", "c"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Hour)
	}
	pinned, err := s.Acquire(t.Context(), Identity{Key: "a"}, false, emptyPopulate)
	require.NoError(t, err)
	defer pinned.Release()

	report, err := s.Collect(t.Context(), Policy{MaxUsedSpace: 2*mib + mib/2})
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)
	require.Equal(t, "b", report.Removed[0].Identity.Key, "the pinned oldest volume is skipped")

	// Everything left is either pinned or used within the keep duration.
	report, err = s.Collect(t.Context(), Policy{MaxUsedSpace: 1, KeepDuration: 2 * time.Hour})
	require.NoError(t, err)
	require.Empty(t, report.Removed)
}

func TestCollectAllRemovesEverythingUnpinned(t *testing.T) {
	s := openStore(t, t.TempDir(), nil)
	for _, key := range []string{"a", "b", "c"} {
		use(t, s, Identity{Key: key}, true, "f", 1)
	}
	pinned, err := s.Acquire(t.Context(), Identity{Key: "c"}, false, emptyPopulate)
	require.NoError(t, err)
	defer pinned.Release()

	report, err := s.Collect(t.Context(), Policy{All: true, KeepDuration: time.Hour})
	require.NoError(t, err)
	require.Len(t, report.Removed, 2)
	require.NoError(t, s.WaitRemovals(t.Context()))
	entries, _, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "c", entries[0].Identity.Key)
}

func TestCollectCountsPendingRemovals(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()
	s := openStore(t, dir, clock)
	gate := make(chan struct{})
	s.beforeRemove = func() { <-gate }
	for _, key := range []string{"a", "b", "c", "d"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}
	policy := Policy{MaxUsedSpace: 3*mib + mib/2}

	report, err := s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)

	_, used, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, used, int64(4*mib), "bytes still being removed count as used")

	report, err = s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Empty(t, report.Removed, "removal already pending meets the target; no more victims")

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.WaitRemovals(ctx), context.DeadlineExceeded)

	close(gate)
	require.NoError(t, s.WaitRemovals(t.Context()))
	ents, err := os.ReadDir(filepath.Join(dir, "v1", "trash"))
	require.NoError(t, err)
	require.Empty(t, ents)
	_, used, err = s.Usage(t.Context())
	require.NoError(t, err)
	require.Less(t, used, int64(3*mib+mib/2))
}

func TestCollectOtherFormatFirst(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()
	s := openStore(t, dir, clock)
	use(t, s, Identity{Key: "current"}, true, "f", mib)

	other := filepath.Join(dir, "v2", "volumes", "abc", "fs")
	require.NoError(t, os.MkdirAll(other, 0o755))
	writeFile(t, filepath.Join(other, "f"), mib)

	entries, used, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, used, int64(2*mib), "other formats count toward usage")
	require.Len(t, entries, 2)

	report, err := s.Collect(t.Context(), Policy{MaxUsedSpace: mib + mib/2})
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)
	require.Equal(t, 2, report.Removed[0].Format, "another format's volume goes first, though it was never used here")
	require.NoError(t, s.WaitRemovals(t.Context()))
	require.NoDirExists(t, filepath.Join(dir, "v2", "volumes", "abc"))
}

func TestCollectStopsWhenCancelled(t *testing.T) {
	s := openStore(t, t.TempDir(), nil)
	for _, key := range []string{"a", "b"} {
		use(t, s, Identity{Key: key}, true, "f", 1)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := s.Collect(ctx, Policy{All: true})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, report.Removed)

	entries, _, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 2, "a cut collection leaves the directory as it was")
}

func TestHandOffToAnotherEngine(t *testing.T) {
	dirA := t.TempDir()
	ident := Identity{Namespace: "mainClient", Key: "go-build", Source: "sha256:abc"}
	a := openStore(t, dirA, nil)
	use(t, a, ident, true, "cached", 10)
	require.NoError(t, a.Close())

	// The next engine gets a copy of the whole directory, as from a
	// Namespace fork, including A's lock file.
	dirB := filepath.Join(t.TempDir(), "copy")
	out, err := exec.Command("cp", "-a", dirA, dirB).CombinedOutput()
	require.NoError(t, err, string(out))

	b := openStore(t, dirB, nil)
	pin, err := b.Acquire(t.Context(), ident, false, func(context.Context, string) error {
		return errors.New("must not recreate a volume that came with the directory")
	})
	require.NoError(t, err)
	defer pin.Release()
	require.FileExists(t, filepath.Join(pin.Path(), "cached"))
}

func TestCloseKeepsOwnershipWhilePinned(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	require.NoError(t, err)
	pin, err := s.Acquire(t.Context(), Identity{Key: "k"}, true, emptyPopulate)
	require.NoError(t, err)

	require.ErrorIs(t, s.Close(), ErrPinned, "a mounted volume keeps the directory owned")
	_, err = Open(dir, Options{})
	require.ErrorIs(t, err, ErrOwned, "so no other owner can collect it")

	pin.Release()
	require.NoError(t, s.Close())
	s2, err := Open(dir, Options{})
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}

func TestOpenRemovesOtherFormatsDebris(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"v2/tmp/half-made", "v2/trash/half-gone"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		writeFile(t, filepath.Join(dir, sub, "f"), 10)
	}
	openStore(t, dir, nil)
	for _, sub := range []string{"v2/tmp", "v2/trash"} {
		ents, err := os.ReadDir(filepath.Join(dir, sub))
		require.NoError(t, err)
		require.Empty(t, ents, sub)
	}
}

// A writer that pins while a measurement is clearing the dirty marker must
// keep its marker: otherwise its writes hide behind the old size.
func TestClearingDirtyDoesNotLoseANewWriter(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	ident := Identity{Key: "k"}
	use(t, s, ident, true, "a", 1)

	var writer sync.WaitGroup
	s.beforeClearDirty = func() {
		s.beforeClearDirty = nil
		writer.Add(1)
		go func() {
			defer writer.Done()
			pin, err := s.Acquire(context.Background(), ident, true, emptyPopulate)
			if err == nil {
				pin.Release()
			}
		}()
		// Give the writer every chance to get in before the marker goes.
		time.Sleep(100 * time.Millisecond)
	}
	_, _, err := s.Usage(t.Context())
	require.NoError(t, err)
	writer.Wait()
	require.FileExists(t, filepath.Join(volumeDir(dir, ident), "dirty"), "the new writer's marker survives the measurement")
}

// A removal that finishes while Collect decides must not count twice: the
// bytes it freed were in the usage Collect read.
func TestCollectDoesNotOvershootWhenARemovalFinishes(t *testing.T) {
	clock := newFakeClock()
	s := openStore(t, t.TempDir(), clock)
	gate := make(chan struct{})
	s.beforeRemove = func() { <-gate }
	for _, key := range []string{"a", "b", "c", "d"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}
	policy := Policy{MaxUsedSpace: 3*mib + mib/2}
	report, err := s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)

	// The pending removal finishes after the next Collect read its usage.
	s.afterUsage = func() {
		s.afterUsage = nil
		close(gate)
		require.NoError(t, s.WaitRemovals(context.Background()))
	}
	report, err = s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Empty(t, report.Removed, "the usage it read already counted the finished removal as pending")
}

func TestFailedRemovalStaysCounted(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()
	s := openStore(t, dir, clock)
	s.removeAll = func(string) error { return errors.New("disk on fire") }
	for _, key := range []string{"a", "b"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}
	report, err := s.Collect(t.Context(), Policy{MaxUsedSpace: mib + mib/2})
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)

	err = s.WaitRemovals(t.Context())
	require.ErrorContains(t, err, "disk on fire", "a failed removal is reported")
	_, used, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, used, int64(2*mib), "what it couldn't remove still counts")
	require.NoError(t, s.WaitRemovals(t.Context()), "reported once")
}

// An acquisition still creating its volume holds the directory like a pin:
// another owner's open would otherwise sweep the volume being made.
func TestCloseWaitsOutAcquisitionInProgress(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	require.NoError(t, err)

	populating := make(chan struct{})
	finish := make(chan struct{})
	type result struct {
		pin *Pin
		err error
	}
	done := make(chan result, 1)
	go func() {
		pin, err := s.Acquire(context.Background(), Identity{Key: "k"}, true, func(context.Context, string) error {
			close(populating)
			<-finish
			return nil
		})
		done <- result{pin, err}
	}()
	<-populating

	require.ErrorIs(t, s.Close(), ErrPinned, "the volume being created keeps the directory owned")
	_, err = Open(dir, Options{})
	require.ErrorIs(t, err, ErrOwned)

	close(finish)
	r := <-done
	require.NoError(t, r.err)
	r.pin.Release()
	require.NoError(t, s.Close())
	require.True(t, s.Closed())

	_, err = s.Acquire(t.Context(), Identity{Key: "k"}, false, emptyPopulate)
	require.ErrorIs(t, err, ErrClosed, "a closed store takes no new acquisitions")
}

// Space a failed removal left behind is stuck, not about to be freed: later
// collections still evict to get under the cap.
func TestFailedRemovalDoesNotStopLaterEviction(t *testing.T) {
	clock := newFakeClock()
	s := openStore(t, t.TempDir(), clock)
	s.removeAll = func(string) error { return errors.New("disk on fire") }
	for _, key := range []string{"a", "b", "c"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}
	policy := Policy{MaxUsedSpace: 2*mib + mib/2}

	report, err := s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)
	require.Error(t, s.WaitRemovals(t.Context()))

	report, err = s.Collect(t.Context(), policy)
	require.NoError(t, err)
	require.Len(t, report.Removed, 1, "usage is still over the cap, and nothing is being removed")
	require.Equal(t, "b", report.Removed[0].Identity.Key)
}

// If what a failed removal left can't be measured, none of it counts as freed.
func TestUnmeasurableFailedRemovalCountsInFull(t *testing.T) {
	clock := newFakeClock()
	s := openStore(t, t.TempDir(), clock)
	s.removeAll = func(string) error { return errors.New("disk on fire") }
	s.trashSize = func(context.Context, string) (int64, error) { return 0, errors.New("unreadable") }
	for _, key := range []string{"a", "b"} {
		use(t, s, Identity{Key: key}, true, "f", mib)
		clock.Advance(time.Minute)
	}
	report, err := s.Collect(t.Context(), Policy{MaxUsedSpace: mib + mib/2})
	require.NoError(t, err)
	require.Len(t, report.Removed, 1)

	err = s.WaitRemovals(t.Context())
	require.ErrorContains(t, err, "disk on fire")
	require.ErrorContains(t, err, "unreadable", "the measurement failure is reported too")
	_, used, err := s.Usage(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, used, int64(2*mib), "the removed volume still counts in full")
}
