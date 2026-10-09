package core

import (
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// releaseCountingRef is a snapshot whose mounts count their releases. Its
// mount is a plain bind of a directory, which MountRef reads in place.
type releaseCountingRef struct {
	bkcache.ImmutableRef
	dir        string
	releases   int
	releaseErr error
}

func (r *releaseCountingRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return releaseCountingMountable{r}, nil
}

type releaseCountingMountable struct{ ref *releaseCountingRef }

func (m releaseCountingMountable) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: m.ref.dir, Options: []string{"rbind"}}}, func() error {
		m.ref.releases++
		return m.ref.releaseErr
	}, nil
}

// backgroundReleaseRef releases its mounts in the background, as immutable
// refs do; the test runs the handed-off releases.
type backgroundReleaseRef struct {
	*releaseCountingRef
	pending []func() error
}

func (r *backgroundReleaseRef) ReleaseInBackground(release func() error) {
	r.pending = append(r.pending, release)
}

// Closing a mount of a ref that releases in the background hands the release
// off and returns without waiting for it; the release still runs, with its
// error going to the releaser rather than the caller.
func TestMountRefReleasesInBackground(t *testing.T) {
	injected := errors.New("injected release failure")
	ref := &backgroundReleaseRef{releaseCountingRef: &releaseCountingRef{dir: t.TempDir(), releaseErr: injected}}

	err := MountRef(context.Background(), ref, func(string, *mount.Mount) error { return nil }, mountRefAsReadOnly)
	require.NoError(t, err, "the release error is not the caller's")
	require.Zero(t, ref.releases, "the caller does not wait for the release")
	require.Len(t, ref.pending, 1)
	require.ErrorIs(t, ref.pending[0](), injected)
	require.Equal(t, 1, ref.releases)

	readErr := errors.New("injected read failure")
	err = MountRef(context.Background(), ref, func(string, *mount.Mount) error { return readErr }, mountRefAsReadOnly)
	require.ErrorIs(t, err, readErr)
	require.Len(t, ref.pending, 2, "a failed read's mount is released in the background too")
}

// A ref that cannot release in the background, such as a mutable ref that is
// committed after its mount closes, is released before the close returns.
func TestMountRefReleasesInPlaceWithoutBackgroundReleaser(t *testing.T) {
	ref := &releaseCountingRef{dir: t.TempDir()}
	require.NoError(t, MountRef(context.Background(), ref, func(string, *mount.Mount) error { return nil }))
	require.Equal(t, 1, ref.releases)
}
