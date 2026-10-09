package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// countingMountRef is a snapshot whose read-only mounts are counted. Its
// mount is a plain bind of a directory, which MountRef reads in place.
type countingMountRef struct {
	bkcache.ImmutableRef
	id       string
	dir      string
	mountErr error
	mounts   atomic.Int64
	releases atomic.Int64
}

func newCountingMountRef(t *testing.T, id string) *countingMountRef {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte(id), 0o644))
	return &countingMountRef{id: id, dir: dir}
}

func (r *countingMountRef) SnapshotID() string { return r.id }

func (r *countingMountRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	if r.mountErr != nil {
		return nil, r.mountErr
	}
	r.mounts.Add(1)
	return countingMountable{r}, nil
}

type countingMountable struct{ ref *countingMountRef }

func (m countingMountable) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: m.ref.dir, Options: []string{"rbind"}}}, func() error {
		m.ref.releases.Add(1)
		return nil
	}, nil
}

func readShared(ctx context.Context, ref bkcache.Ref) (string, error) {
	var data []byte
	err := MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		var err error
		data, err = os.ReadFile(filepath.Join(root, "f"))
		return err
	}, mountRefAsReadOnly, mountRefShared)
	return string(data), err
}

// Within a read mount scope, shared reads of one snapshot use one mount, even
// through different refs to it, until the scope closes. Each snapshot gets its
// own mount, and reads that do not opt in mount as before.
func TestReadMountScopeSharesMountPerSnapshot(t *testing.T) {
	ctx := context.Background()
	a := newCountingMountRef(t, "a")
	aAgain := &countingMountRef{id: "a", dir: t.TempDir()}
	b := newCountingMountRef(t, "b")

	scoped, closeScope := withReadMountScope(ctx)
	for range 3 {
		data, err := readShared(scoped, a)
		require.NoError(t, err)
		require.Equal(t, "a", data)
	}
	data, err := readShared(scoped, aAgain)
	require.NoError(t, err)
	require.Equal(t, "a", data, "another ref to the snapshot reads the shared mount")
	data, err = readShared(scoped, b)
	require.NoError(t, err)
	require.Equal(t, "b", data)
	require.NoError(t, MountRef(scoped, a, func(string, *mount.Mount) error { return nil }, mountRefAsReadOnly))

	require.EqualValues(t, 2, a.mounts.Load(), "one shared mount and one unshared")
	require.EqualValues(t, 1, a.releases.Load(), "the unshared mount is released at once")
	require.Zero(t, aAgain.mounts.Load())
	require.EqualValues(t, 1, b.mounts.Load())
	require.Zero(t, b.releases.Load())

	nestedCtx, closeNested := withReadMountScope(scoped)
	_, err = readShared(nestedCtx, b)
	require.NoError(t, err)
	require.NoError(t, closeNested())
	require.EqualValues(t, 1, b.mounts.Load(), "a nested scope uses the outer one")
	require.Zero(t, b.releases.Load(), "closing a nested scope leaves the outer one's mounts")

	require.NoError(t, closeScope())
	require.EqualValues(t, 2, a.releases.Load())
	require.EqualValues(t, 1, b.releases.Load())

	_, err = readShared(scoped, a)
	require.NoError(t, err)
	require.EqualValues(t, 3, a.mounts.Load(), "after the scope closes, a read mounts on its own")
	require.EqualValues(t, 3, a.releases.Load())

	_, err = readShared(ctx, b)
	require.NoError(t, err)
	require.EqualValues(t, 2, b.mounts.Load(), "outside a scope, a read mounts on its own")
	require.EqualValues(t, 2, b.releases.Load())
}

// Concurrent shared reads of one snapshot mount it once.
func TestReadMountScopeConcurrentReaders(t *testing.T) {
	ctx, closeScope := withReadMountScope(context.Background())
	ref := newCountingMountRef(t, "a")
	var wg sync.WaitGroup
	errs := make([]error, 32)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := readShared(ctx, ref)
			if err == nil && data != "a" {
				err = errors.New("read " + data)
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	require.NoError(t, errors.Join(errs...))
	require.EqualValues(t, 1, ref.mounts.Load())
	require.NoError(t, closeScope())
	require.EqualValues(t, 1, ref.releases.Load())
}

// A failed mount is not shared: its reader gets the error and the next
// reader mounts again. A failed read keeps the mount for the others.
func TestReadMountScopeMountFailure(t *testing.T) {
	ctx, closeScope := withReadMountScope(context.Background())
	ref := newCountingMountRef(t, "a")
	injected := errors.New("injected mount failure")

	ref.mountErr = injected
	_, err := readShared(ctx, ref)
	require.ErrorIs(t, err, injected)
	ref.mountErr = nil

	readErr := errors.New("injected read failure")
	err = MountRef(ctx, ref, func(string, *mount.Mount) error { return readErr }, mountRefShared)
	require.ErrorIs(t, err, readErr)
	data, err := readShared(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "a", data)
	require.EqualValues(t, 1, ref.mounts.Load())
	require.Zero(t, ref.releases.Load())

	require.NoError(t, closeScope())
	require.EqualValues(t, 1, ref.releases.Load())
}

// Closing a scope while a reader is still using its mount leaves the mount
// to that reader, which releases it when it finishes.
func TestReadMountScopeCloseDuringRead(t *testing.T) {
	ctx, closeScope := withReadMountScope(context.Background())
	ref := newCountingMountRef(t, "a")
	reading, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
			close(reading)
			<-finish
			_, err := os.ReadFile(filepath.Join(root, "f"))
			return err
		}, mountRefShared)
	}()
	<-reading
	require.NoError(t, closeScope())
	require.Zero(t, ref.releases.Load(), "the mount in use is not released")
	close(finish)
	require.NoError(t, <-done)
	require.EqualValues(t, 1, ref.releases.Load(), "its reader releases it")
}
