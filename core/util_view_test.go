package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLeavesDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(filepath.Join(app, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "main.txt"), []byte("main"), 0o644))
	require.NoError(t, os.Symlink("../main.txt", filepath.Join(app, "nested", "inner")))
	require.NoError(t, os.Symlink("nested", filepath.Join(app, "alias")))
	require.NoError(t, os.Symlink("../other.txt", filepath.Join(app, "up")))
	require.NoError(t, os.Symlink("/main.txt", filepath.Join(app, "abs")))
	require.NoError(t, os.Symlink("nested/../../x", filepath.Join(app, "climb")))
	require.NoError(t, os.Symlink("loop", filepath.Join(app, "loop")))

	for _, tc := range []struct {
		p      string
		leaves bool
	}{
		{"main.txt", false},
		{"nested/inner", false},
		{"alias/inner", false},
		{"missing/x", false},
		{"../main.txt", false}, // ".." in the path itself stops at the directory, as written
		{"up", true},
		{"abs", true},
		{"climb", true},
		{"alias/../up", true},
	} {
		leaves, err := leavesDir(app, tc.p)
		require.NoError(t, err, tc.p)
		require.Equal(t, tc.leaves, leaves, tc.p)
	}

	_, err := leavesDir(app, "loop")
	require.Error(t, err)
}

func TestResolveInDirStaysInDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0o755))
	require.NoError(t, os.Symlink("../other.txt", filepath.Join(app, "up")))

	for _, p := range []string{"..", "../", "../..", ".", "/"} {
		got, err := ResolveInDir(root, "/app", p)
		require.NoError(t, err, p)
		require.Equal(t, app, got, p)
		got, err = ResolveInDirWithoutFinalSymlink(root, "/app", p)
		require.NoError(t, err, p)
		require.Equal(t, app, got, p)
	}

	got, err := ResolveInDir(root, "/app", "up")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(app, "other.txt"), got)
	got, err = ResolveInDirWithoutFinalSymlink(root, "/app", "up")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(app, "up"), got)
}

func TestWithLinksConfined(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("inside"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), []byte("outside"), 0o644))
	require.NoError(t, os.Symlink("inside.txt", filepath.Join(dir, "within")))

	// No link leaves the directory: the callback gets it as it is.
	require.NoError(t, withLinksConfined(dir, func(p string) error {
		require.Equal(t, dir, p)
		return nil
	}))

	require.NoError(t, os.Symlink("/inside.txt", filepath.Join(dir, "sub", "abs")))
	require.NoError(t, os.Symlink("../../inside.txt", filepath.Join(dir, "sub", "up")))
	require.NoError(t, os.Symlink("abs", filepath.Join(dir, "sub", "chain")))
	require.NoError(t, os.Symlink("/sub", filepath.Join(dir, "subdir")))
	require.NoError(t, os.Symlink("loop", filepath.Join(dir, "loop")))
	require.NoError(t, withLinksConfined(dir, func(p string) error {
		require.NotEqual(t, dir, p)
		for _, name := range []string{"within", "sub/abs", "sub/up", "sub/chain", "subdir/abs"} {
			got, err := os.ReadFile(filepath.Join(p, name))
			require.NoError(t, err, name)
			require.Equal(t, "inside", string(got), name)
		}
		target, err := os.Readlink(filepath.Join(p, "within"))
		require.NoError(t, err)
		require.Equal(t, "inside.txt", target, "links within the directory are kept as they are")
		return nil
	}))
}

func TestOutsideInputsDigest(t *testing.T) {
	t.Parallel()

	// tree lays out a root with app/ (and lib/, equal to it) and the given
	// outside files and links.
	tree := func(files, links map[string]string) string {
		root := t.TempDir()
		for _, d := range []string{"app", "lib"} {
			require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, d, "main.txt"), []byte("main"), 0o644))
		}
		for name, contents := range files {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644))
		}
		for name, target := range links {
			require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
		}
		return root
	}
	digest := func(root, dir string) string {
		dgst, err := outsideInputsDigest(root, filepath.Join(root, dir))
		require.NoError(t, err)
		return dgst
	}

	none := digest(tree(nil, nil), "app")
	require.Equal(t, none, digest(tree(map[string]string{"other.txt": "x"}, nil), "app"), "other files around it don't count")
	require.Equal(t, none, digest(tree(nil, nil), "lib"), "without ignore files, where it is doesn't count")

	passwd := digest(tree(map[string]string{"etc/passwd": "app:x:1000:1000::/:/bin/sh\n"}, nil), "app")
	require.NotEqual(t, none, passwd)
	require.NotEqual(t, passwd, digest(tree(map[string]string{"etc/passwd": "app:x:2000:2000::/:/bin/sh\n"}, nil), "app"))
	require.NotEqual(t, none, digest(tree(map[string]string{"etc/group": "app:x:1000:\n"}, nil), "app"))

	ignored := digest(tree(map[string]string{".gitignore": "*.log\n"}, nil), "app")
	require.NotEqual(t, none, ignored)
	require.NotEqual(t, ignored, digest(tree(map[string]string{".gitignore": "*.tmp\n"}, nil), "app"))
	require.Equal(t, ignored, digest(tree(map[string]string{".gitignore": "*.log\n", "other.txt": "x"}, nil), "app"))
	require.NotEqual(t, ignored, digest(tree(map[string]string{".gitignore": "*.log\n"}, nil), "lib"),
		"with ignore files above it, where it is counts: their patterns match by path")
	require.NotEqual(t, none, digest(tree(map[string]string{".ignore": "*.log\n"}, nil), "app"))

	// A repository above the directory, or at its root, means it isn't keyed
	// by content at all.
	for _, files := range []map[string]string{
		{".git/HEAD": "ref: refs/heads/main\n"},
		{".git": "gitdir: elsewhere\n"},
		{"app/.git/HEAD": "ref: refs/heads/main\n"},
		{"app/nested/.git": "gitdir: ../../../elsewhere\n"},
		{"HEAD": "ref: refs/heads/main\n", "objects/.keep": "", "refs/.keep": ""},
		{"HEAD": "ref: refs/heads/main\n", "commondir": "../common\n"},
		{"app/HEAD": "ref: refs/heads/main\n", "app/objects/.keep": ""},
		{"app/sub/HEAD": "ref: refs/heads/main\n", "app/sub/refs/.keep": ""},
	} {
		root := tree(files, nil)
		_, err := outsideInputsDigest(root, filepath.Join(root, "app"))
		require.ErrorIs(t, err, errInRepository, "%v", files)
	}
	for _, files := range []map[string]string{
		{"HEAD": "x"},
		{"gitdir": "x"},
		{".jj/repo": "x"},
		{"app/.jj/repo": "x"},
		{"app/deep/commondir": "x"},
	} {
		root := tree(files, nil)
		_, err := outsideInputsDigest(root, filepath.Join(root, "app"))
		require.ErrorIs(t, err, errInRepository, "%v", files)
	}

	linked := func(rules string) string {
		return digest(tree(map[string]string{"rules": rules}, map[string]string{"app/.gitignore": "../rules"}), "app")
	}
	require.NotEqual(t, linked("*.log\n"), linked("*.tmp\n"), "an ignore file linked from outside counts")

	// A pipe or a device behind an ignore file is never read, which need not
	// end: the digest fails, so the directory falls back to recipe keying.
	fifo := tree(nil, nil)
	require.NoError(t, syscall.Mkfifo(filepath.Join(fifo, "pipe"), 0o644))
	require.NoError(t, os.Symlink("pipe", filepath.Join(fifo, ".gitignore")))
	done := make(chan error, 1)
	go func() {
		_, err := outsideInputsDigest(fifo, filepath.Join(fifo, "app"))
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		// Unblock the reader so the test can end.
		w, err := os.OpenFile(filepath.Join(fifo, "pipe"), os.O_WRONLY, 0)
		if err == nil {
			w.Close()
		}
		t.Fatal("reading an ignore file that is a pipe blocked")
	}

	loop := tree(nil, map[string]string{".gitignore": ".gitignore"})
	_, err := outsideInputsDigest(loop, filepath.Join(loop, "app"))
	require.Error(t, err)
}
