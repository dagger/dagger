//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	continuityfs "github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
)

func TestGetLayerDelta(t *testing.T) {
	overlay := func(lower []string, upper string) mount.Mount {
		reversed := make([]string, len(lower))
		for i, l := range lower {
			reversed[len(lower)-1-i] = l
		}
		opts := []string{"lowerdir=" + strings.Join(reversed, ":")}
		if upper != "" {
			opts = append(opts, "upperdir="+upper, "workdir=/work")
		}
		return mount.Mount{Type: "overlay", Source: "overlay", Options: opts}
	}
	lower := []mount.Mount{overlay([]string{"/l/0", "/l/1"}, "/l/2")}

	// Several layers above lower.
	delta, err := GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/l/2", "/l/3"}, "/l/4")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/3", "/l/4"}}, delta)

	// One layer is GetUpperdir's answer.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/l/2"}, "/l/3")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/3"}}, delta)

	// A bind-mounted single layer below.
	delta, err = GetLayerDelta([]mount.Mount{{Type: "bind", Source: "/l/0"}}, []mount.Mount{overlay([]string{"/l/0", "/l/1"}, "/l/2")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/1", "/l/2"}}, delta)

	// Siblings on a common ancestor.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/x/2"}, "/x/3")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Lower: []string{"/l/2"}, Upper: []string{"/x/2", "/x/3"}}, delta)

	// An ancestor of lower.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0"}, "/l/1")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Lower: []string{"/l/2"}}, delta)

	// Nothing shared, or the very same layers.
	_, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/x/0", "/l/1"}, "/l/2")})
	require.Error(t, err)
	delta, err = GetLayerDelta(lower, lower)
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Same: true}, delta)
	require.False(t, delta.Empty())
	require.NoError(t, WalkLayerDeltaChanges(context.Background(), func(continuityfs.ChangeKind, string, os.FileInfo, error) error {
		t.Fatal("the same layers have no changes")
		return nil
	}, delta, t.TempDir(), t.TempDir(), CompareInodeThenContent))
}

// Cancellation stops the listing of an added directory, which addDirChanges
// walks without checking the context itself.
func TestWalkLayerDeltaChangesCancelsAddedDir(t *testing.T) {
	// Two layers, so the walk classifies their candidates instead of
	// walking a single upperdir; the views are plain directories.
	first, second, upper := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(first, "added"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(second, "other"), nil, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(upper, "added"), 0o755))
	for i := range 100 {
		require.NoError(t, os.WriteFile(filepath.Join(upper, "added", fmt.Sprintf("f%03d", i)), nil, 0o644))
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	canceled := errors.New("canceled by the test")
	var changes []string
	err := WalkLayerDeltaChanges(ctx, func(_ continuityfs.ChangeKind, path string, _ os.FileInfo, _ error) error {
		changes = append(changes, path)
		cancel(canceled)
		return nil
	}, LayerDelta{Upper: []string{first, second}}, upper, t.TempDir(), CompareInodeThenContent)
	require.ErrorIs(t, err, canceled)
	require.Equal(t, []string{"/added"}, changes)
}
