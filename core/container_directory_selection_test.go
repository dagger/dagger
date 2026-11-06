package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
				if kind == "file" {
					mount = selectionFixtureMount(t, "/data/alias/value", child, "value")
				} else if kind == "resource" {
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
