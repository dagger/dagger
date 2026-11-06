package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/util/layercopy"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func selectionFixture(t *testing.T, files, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	}
	for name, target := range links {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.Symlink(target, path))
	}
	return root
}

func selectionFixtureMount(t *testing.T, target, root, path string) *containerSelectionMount {
	t.Helper()
	return &containerSelectionMount{target: target, load: func() (*containerSelectionBacking, error) {
		handle, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { _ = handle.Close() })
		return &containerSelectionBacking{root: handle, path: path}, nil
	}}
}

func selectionRead(t *testing.T, source *containerSelectionSource, name string) string {
	t.Helper()
	f, err := source.Open(name, nil)
	require.NoError(t, err)
	defer f.Close()
	contents, err := os.ReadFile(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	require.NoError(t, err)
	return string(contents)
}

func TestContainerSelectionNestedMountAliases(t *testing.T) {
	for _, alias := range []string{"/actual", "../actual"} {
		for _, kind := range []string{"directory", "file", "resource"} {
			t.Run(alias+"/"+kind, func(t *testing.T) {
				base := selectionFixture(t, map[string]string{"actual/value": "hidden", "wrong/value": "wrong"}, map[string]string{"data/alias": "/wrong"})
				parent := selectionFixture(t, nil, map[string]string{"alias": alias})
				child := selectionFixture(t, map[string]string{"value": "mounted"}, nil)
				mount := selectionFixtureMount(t, "/data/alias", child, ".")
				switch kind {
				case "file":
					mount = selectionFixtureMount(t, "/data/alias/value", child, "value")
				case "resource":
					mount = &containerSelectionMount{target: "/data/alias"}
				}
				source := &containerSelectionSource{
					base: selectionFixtureMount(t, "/", base, "."),
					mounts: []*containerSelectionMount{
						selectionFixtureMount(t, "/data", parent, "."), mount,
					},
				}
				t.Cleanup(source.Close)
				if kind == "resource" {
					_, err := source.Open("/actual/value", nil)
					require.ErrorContains(t, err, "cannot retrieve path from non-snapshot mount /actual")
				} else {
					require.Equal(t, "mounted", selectionRead(t, source, "/actual/value"))
				}
			})
		}
	}
}

func TestContainerSelectionReusesSyntheticParents(t *testing.T) {
	source := &containerSelectionSource{}
	t.Cleanup(source.Close)
	first, err := source.stub("/missing")
	require.NoError(t, err)
	for range 1000 {
		backing, err := source.stub("/missing/.")
		require.NoError(t, err)
		require.Same(t, first, backing)
		f, err := backing.root.OpenFile(".", unix.O_PATH, 0)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	require.Len(t, source.closers, 1, "repeated lookups must not retain more handles")
	source.Close()
	_, err = first.root.OpenFile(".", unix.O_PATH, 0)
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestContainerSelectionDirectMountAlias(t *testing.T) {
	for _, selection := range []string{"/alias", "/"} {
		t.Run(selection, func(t *testing.T) {
			base := selectionFixture(t, map[string]string{
				"value": "wrong", "deep/value": "right", "deep/target/hidden": "hidden",
			}, map[string]string{"alias": "/deep/target"})
			mounted := selectionFixture(t, nil, map[string]string{
				"link": "../value", "absolute": "/deep/value",
			})
			source := &containerSelectionSource{
				base:   selectionFixtureMount(t, "/", base, "."),
				mounts: []*containerSelectionMount{selectionFixtureMount(t, "/alias", mounted, ".")},
			}
			t.Cleanup(source.Close)
			dst := t.TempDir()
			copier, err := layercopy.NewCopier(layercopy.Mount{Root: dst})
			require.NoError(t, err)
			defer copier.Close()
			require.NoError(t, copier.CopySelection(context.Background(), "", selection, layercopy.SelectionOptions{Source: source}))
			if selection == "/" {
				dst = filepath.Join(dst, "alias")
			}
			for _, name := range []string{"link", "absolute"} {
				content, err := os.ReadFile(filepath.Join(dst, name))
				require.NoError(t, err)
				require.Equal(t, "right", string(content))
			}
			_, err = os.Stat(filepath.Join(dst, "hidden"))
			require.ErrorIs(t, err, os.ErrNotExist)
			require.Equal(t, "/deep/target", source.mounts[0].target)
		})
	}
}

func TestContainerSelectionFilesMountStartsEmpty(t *testing.T) {
	source := &containerSelectionSource{}
	t.Cleanup(source.Close)
	_, err := source.stub("/files/old/child")
	require.NoError(t, err)
	backing, err := source.emptyFilesMount()
	require.NoError(t, err)
	f, err := backing.root.Open(".")
	require.NoError(t, err)
	defer f.Close()
	names, err := f.Readdirnames(-1)
	require.NoError(t, err)
	require.Empty(t, names, "a files mount must not inherit earlier mountpoint stubs")
}

func TestContainerSelectionRejectsOpaqueMountTargetParents(t *testing.T) {
	base := selectionFixture(t, map[string]string{"app/value": "hidden"}, nil)
	child := selectionFixture(t, map[string]string{"value": "mounted"}, nil)
	mount := selectionFixtureMount(t, "/cache/redirect", child, ".")
	load := mount.load
	loads := 0
	mount.load = func() (*containerSelectionBacking, error) {
		loads++
		return load()
	}
	// The cache could contain redirect -> /app. Its contents are opaque,
	// so a raw /cache/redirect target cannot establish what hides /app.
	source := &containerSelectionSource{
		base: selectionFixtureMount(t, "/", base, "."),
		mounts: []*containerSelectionMount{
			{target: "/cache"}, mount,
		},
	}
	t.Cleanup(source.Close)
	for _, name := range []string{"/app/value", "/cache/redirect/value", "/app/value"} {
		f, err := source.Open(name, nil)
		require.Nil(t, f, "an uncertain mount location must not expose underlying files")
		require.ErrorIs(t, err, errOpaqueSelectionParent)
		require.ErrorContains(t, err, "non-snapshot mount /cache")
	}
	_, err := source.Children("/", nil)
	require.ErrorIs(t, err, errOpaqueSelectionParent)
	require.Zero(t, loads, "reject the unknown target before loading its snapshot")
}
