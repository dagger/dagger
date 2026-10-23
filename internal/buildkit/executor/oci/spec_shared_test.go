package oci

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

type fakeSharedMounter struct {
	root     string
	acquired int
	released int
}

func (f *fakeSharedMounter) Mount(context.Context, bool) (executor.MountableRef, error) {
	return nil, errors.New("a shared read-only mount must not make a mount of its own")
}

func (f *fakeSharedMounter) MountShared(context.Context) (string, mount.Mount, func(), error) {
	f.acquired++
	return f.root, mount.Mount{Type: "bind", Source: f.root}, func() { f.released++ }, nil
}

func TestSharedReadonlyMount(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "go/pkg/mod/m@v1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go/pkg/mod/m@v1/a.go"), nil, 0o644))
	require.NoError(t, os.Symlink("/go/pkg/mod/m@v1", filepath.Join(root, "abs")))

	for _, tc := range []struct {
		name     string
		selector string
		want     string
	}{
		{"whole snapshot", "/", root},
		{"directory", "/go/pkg/mod/m@v1", filepath.Join(root, "go/pkg/mod/m@v1")},
		{"file", "/go/pkg/mod/m@v1/a.go", filepath.Join(root, "go/pkg/mod/m@v1/a.go")},
		{"symlink resolved inside the snapshot", "/abs/a.go", filepath.Join(root, "go/pkg/mod/m@v1/a.go")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSharedMounter{root: root}
			mnt, release, ok, err := sharedReadonlyMount(t.Context(), executor.Mount{
				Src:      src,
				Selector: tc.selector,
				Dest:     "/in",
				Readonly: true,
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "/in", mnt.Destination)
			require.Equal(t, "bind", mnt.Type)
			require.Equal(t, tc.want, mnt.Source)
			require.Equal(t, []string{"rbind", "ro"}, mnt.Options)
			require.Equal(t, 1, src.acquired)
			require.Equal(t, 0, src.released)
			require.NoError(t, release())
			require.Equal(t, 1, src.released)
		})
	}

	t.Run("missing selector", func(t *testing.T) {
		src := &fakeSharedMounter{root: root}
		_, _, ok, err := sharedReadonlyMount(t.Context(), executor.Mount{
			Src:      src,
			Selector: "/nope",
			Dest:     "/in",
			Readonly: true,
		})
		require.True(t, ok)
		require.ErrorIs(t, err, os.ErrNotExist)
		var perr *os.PathError
		require.ErrorAs(t, err, &perr)
		require.Equal(t, "/nope", perr.Path)
		require.Equal(t, 1, src.released)
	})

	t.Run("writable", func(t *testing.T) {
		src := &fakeSharedMounter{root: root}
		_, _, ok, err := sharedReadonlyMount(t.Context(), executor.Mount{
			Src:      src,
			Selector: "/go",
			Dest:     "/in",
		})
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, 0, src.acquired)
	})
}
