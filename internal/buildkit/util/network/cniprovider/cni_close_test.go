//go:build linux

package cniprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cni "github.com/containerd/go-cni"
	"github.com/stretchr/testify/require"
)

// fakeCNI tears namespaces down without plugins, failing for the ids in fail.
type fakeCNI struct {
	cni.CNI
	mu      sync.Mutex
	removed []string
	fail    map[string]bool
}

func (f *fakeCNI) Remove(_ context.Context, id string, _ string, _ ...cni.NamespaceOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[id] {
		return errors.New("teardown failed")
	}
	f.removed = append(f.removed, id)
	return nil
}

// fakeNS returns a namespace whose file is in dir, torn down through handle.
func fakeNS(t *testing.T, dir, id string, handle cni.CNI) *cniNS {
	t.Helper()
	path := filepath.Join(dir, id)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	// The file isn't a mount: unmounting it would need privileges the
	// package's tests don't run with.
	return &cniNS{id: id, nativeID: path, handle: handle, unmount: func(string) error { return nil }}
}

func TestPoolCloseReportsWhatItTookDown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	handle := &fakeCNI{fail: map[string]bool{"b": true}}
	pool := &cniPool{targetSize: 2}
	ids := []string{"a", "b", "c"}
	next := 0
	pool.newNS = func(context.Context) (*cniNS, error) {
		ns := fakeNS(t, dir, ids[next], handle)
		next++
		return ns, nil
	}

	// Three execs at once take three namespaces, one more than the target,
	// and give them back.
	var held []*cniNS
	for range ids {
		ns, err := pool.get(t.Context())
		require.NoError(t, err)
		held = append(held, ns)
	}
	for _, ns := range held {
		pool.put(ns)
	}

	report := pool.close()
	require.Equal(t, 2, report.Released)
	require.Equal(t, 1, report.Failed)
	require.Equal(t, 3, report.Peak, "the pool grew past its target while three namespaces were in use")
	require.Positive(t, report.Took)
	require.ElementsMatch(t, []string{"a", "c"}, handle.removed)
	require.FileExists(t, filepath.Join(dir, "b"), "a failed teardown keeps its file for the next start")
	require.NoFileExists(t, filepath.Join(dir, "a"))
}

// slowCNI tears a namespace down in delay, counting the teardowns running at
// once.
type slowCNI struct {
	fakeCNI
	delay    time.Duration
	inFlight atomic.Int64
	most     atomic.Int64
}

func (s *slowCNI) Remove(ctx context.Context, id string, path string, opts ...cni.NamespaceOpts) error {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		most := s.most.Load()
		if n <= most || s.most.CompareAndSwap(most, n) {
			break
		}
	}
	time.Sleep(s.delay)
	return s.fakeCNI.Remove(ctx, id, path, opts...)
}

func TestPoolCloseTearsNamespacesDownSideBySide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	handle := &slowCNI{fakeCNI: fakeCNI{fail: map[string]bool{"ns-7": true}}, delay: 20 * time.Millisecond}
	pool := &cniPool{targetSize: 128}
	const n = 32
	for i := range n {
		ns := fakeNS(t, dir, fmt.Sprintf("ns-%d", i), handle)
		ns.pool = pool
		pool.available = append(pool.available, ns)
		pool.actualSize++
	}

	report := pool.close()
	require.Equal(t, n-1, report.Released)
	require.Equal(t, 1, report.Failed)
	require.Len(t, handle.removed, n-1)
	require.Greater(t, handle.most.Load(), int64(1), "teardowns overlap")
	require.LessOrEqual(t, handle.most.Load(), int64(poolCloseWorkers), "at most poolCloseWorkers at once")
	require.FileExists(t, filepath.Join(dir, "ns-7"), "a failed teardown keeps its file for the next start")
	require.NoFileExists(t, filepath.Join(dir, "ns-0"))
	require.Empty(t, pool.available)
	require.Zero(t, pool.actualSize)
}
