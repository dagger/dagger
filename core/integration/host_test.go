package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type HostSuite struct{}

func TestHost(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(HostSuite{})
}

func (HostSuite) TestWorkdir(ctx context.Context, t *testctx.T) {
	t.Run("contains the workdir's content", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "foo"), []byte("bar"), 0o600)
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithWorkdir(dir))

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", c.Host().Directory(".")).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)
	})

	t.Run("does NOT re-sync on each call", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "foo"), []byte("bar"), 0o600)
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithWorkdir(dir))

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", c.Host().Directory(".")).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)

		err = os.WriteFile(filepath.Join(dir, "fizz"), []byte("buzz"), 0o600)
		require.NoError(t, err)

		contents, err = c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", c.Host().Directory(".")).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)
	})
}

func (HostSuite) TestWorkdirExcludeInclude(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt.rar"), []byte("3"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "sub-file"), []byte("goodbye"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(dir))

	t.Run("exclude", func(ctx context.Context, t *testctx.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Exclude: []string{"*.rar"},
		})

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\nb.txt\nsubdir\n", contents)
	})

	t.Run("exclude directory", func(ctx context.Context, t *testctx.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Exclude: []string{"subdir"},
		})

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\nb.txt\nc.txt.rar\n", contents)
	})

	t.Run("include", func(ctx context.Context, t *testctx.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"*.rar"},
		})

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "c.txt.rar\n", contents)
	})

	t.Run("exclude overrides include", func(ctx context.Context, t *testctx.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		})

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\n", contents)
	})

	t.Run("include does not override exclude", func(ctx context.Context, t *testctx.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		})

		contents, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", contents)
	})
}

func (HostSuite) TestDirectoryRelative(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(dir))

	t.Run(". is same as workdir", func(ctx context.Context, t *testctx.T) {
		wdID1, err := c.Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		wdID2, err := c.Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		require.Equal(t, wdID1, wdID2)
	})

	t.Run("./foo is relative to workdir", func(ctx context.Context, t *testctx.T) {
		contents, err := c.Host().Directory("some-dir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, contents)
	})

	t.Run("../ allows escaping", func(ctx context.Context, t *testctx.T) {
		_, err := c.Host().Directory("../").ID(ctx)
		require.NoError(t, err)
	})
}

func (HostSuite) TestDirectoryAbsolute(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir(dir))

	entries, err := c.Host().Directory(filepath.Join(dir, "some-dir")).Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, entries)
}

func (HostSuite) TestDirectoryHome(ctx context.Context, t *testctx.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	subdir := filepath.Join(".cache", "dagger-test-"+identity.NewID())

	require.NoError(t, os.MkdirAll(filepath.Join(home, subdir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, subdir, "some-file"), []byte("hello"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, subdir, "some-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, subdir, "some-dir", "sub-file"), []byte("goodbye"), 0o600))

	c := connect(ctx, t, dagger.WithWorkdir("/tmp"))

	entries, err := c.Host().Directory(filepath.Join("~", subdir, "some-dir")).Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, entries)
}

func (HostSuite) TestDirectoryExcludeInclude(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt.rar"), []byte("3"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "d.txt"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "e.txt"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "f.txt.rar"), []byte("3"), 0o600))

	c := connect(ctx, t)

	t.Run("exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "subdir/"}, entries)
	})

	t.Run("include", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar"}, entries)
	})

	t.Run("exclude overrides include", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt"}, entries)
	})

	t.Run("include does not override exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{}, entries)
	})
}

func (HostSuite) TestDirectoryGitIgnore(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	gitignore := strings.Join([]string{
		"**.txt",
		".git/",
		"!subdir/e.txt",
		"*.md",
		"internal/foo/",
	}, "\n")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt.rar"), []byte("3"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "b.md"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "e.txt"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "g.txt"), []byte("6"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "h.yaml"), []byte("6"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir2"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir2", ".gitignore"), []byte(`*.yaml
.gitignore
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir2", "foo.go"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir2", "bar.txt"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir2", "baz.md"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir2", "bool.yaml"), []byte("1"), 0o600))

	c := connect(ctx, t)

	t.Run("no git ignore by default", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Host().Directory(dir).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git/", ".gitignore", "b.md", "c.txt.rar", "subdir/", "subdir2/"}, entries)

		subDirEntries, err := c.Host().Directory(filepath.Join(dir, "subdir")).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"b.md", "e.txt", "g.txt", "h.yaml"}, subDirEntries)

		subDir2Entries, err := c.Host().Directory(filepath.Join(dir, "subdir2")).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".gitignore", "bar.txt", "baz.md", "bool.yaml", "foo.go"}, subDir2Entries)
	})

	t.Run("apply git ignore", func(ctx context.Context, t *testctx.T) {
		hostDir := c.Host().Directory(dir, dagger.HostDirectoryOpts{Gitignore: true})

		rootHostDir, err := hostDir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".gitignore", "c.txt.rar", "subdir/", "subdir2/"}, rootHostDir)

		subDirEntries, err := hostDir.Directory("subdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"e.txt", "h.yaml"}, subDirEntries)

		subDir2Entries, err := hostDir.Directory("subdir2").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"foo.go"}, subDir2Entries)
	})

	t.Run("correctly apply parent .gitignore when children path is given", func(ctx context.Context, t *testctx.T) {
		subDirEntries, err := c.Host().Directory(filepath.Join(dir, "subdir"), dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"e.txt", "h.yaml"}, subDirEntries)
	})

	t.Run("don't apply .gitignore if no .git found", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(`**/**`), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("1"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("1"), 0o600))

		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".gitignore", "bar.txt", "foo.go"}, entries)
	})

	t.Run("don't load files ignored by .gitignore", func(ctx context.Context, t *testctx.T) {
		hostDir := t.TempDir()

		require.NoError(t, os.MkdirAll(filepath.Join(hostDir, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, ".gitignore"), []byte("bar/\nbaz.txt\n"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(hostDir, "foo/"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "foo/foo.txt"), []byte("1"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(hostDir, "bar/"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "bar/bar.txt"), []byte("1"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "baz.txt"), []byte("1"), 0o600))

		// sanity check!
		rootEntries, err := c.Host().Directory(hostDir, dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git/", ".gitignore", "foo/"}, rootEntries)

		fooEntries, err := c.Host().Directory(filepath.Join(hostDir, "foo/"), dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"foo.txt"}, fooEntries)

		barEntries, err := c.Host().Directory(filepath.Join(hostDir, "bar/"), dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.Error(t, err, fmt.Errorf("expected error, got: %#v (root entries: %#v)", barEntries, rootEntries))
		requireErrOut(t, err, "no such file or directory")
	})

	t.Run("correctly handle excluded gitignore", func(ctx context.Context, t *testctx.T) {
		hostDir := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Gitignore: true,
			Exclude:   []string{".gitignore"},
		})

		rootHostDir, err := hostDir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar", "subdir/", "subdir2/"}, rootHostDir)

		subDirEntries, err := hostDir.Directory("subdir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"e.txt", "h.yaml"}, subDirEntries)

		subDir2Entries, err := hostDir.Directory("subdir2").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"foo.go"}, subDir2Entries)
	})

	t.Run("use gitignores after no git ignores", func(ctx context.Context, t *testctx.T) {
		// tests a weird edge case, where we populate the local dir, but then
		// copy doesn't actually respect gitignores

		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("a.txt\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0o600))

		entries, err := c.Host().Directory(dir).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git/", ".gitignore", "a.txt", "b.txt"}, entries)

		entries, err = c.Host().Directory(dir, dagger.HostDirectoryOpts{Gitignore: true}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git/", ".gitignore", "b.txt"}, entries)
	})
}

func (HostSuite) TestDirectoryCacheBehavior(ctx context.Context, t *testctx.T) {
	baseDir := t.TempDir()
	c := connect(ctx, t)

	tests := []struct {
		name            string
		opts            dagger.HostDirectoryOpts
		expectedEntries []string
		expectedContent string
	}{
		{
			name:            "default aka cache",
			opts:            dagger.HostDirectoryOpts{},
			expectedEntries: []string{"file1.txt"},
			expectedContent: "1",
		},
		{
			name:            "explicit cache",
			opts:            dagger.HostDirectoryOpts{NoCache: false},
			expectedEntries: []string{"file1.txt"},
			expectedContent: "1",
		},
		{
			name:            "explicit no cache",
			opts:            dagger.HostDirectoryOpts{NoCache: true},
			expectedEntries: []string{"file1.txt", "file2.txt"},
			expectedContent: "12",
		},
	}

	for _, test := range tests {
		setup := func() (string, *dagger.Directory) {
			dir := filepath.Join(baseDir, identity.NewID())
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("1"), 0o600))

			directory := c.Host().Directory(dir, test.opts)
			return dir, directory
		}

		t.Run(test.name, func(ctx context.Context, t *testctx.T) {
			dir, directory := setup()
			entries, err := directory.Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"file1.txt"}, entries)

			require.NoError(t, os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("1"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("12"), 0o600))

			entries, err = directory.Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expectedEntries, entries)

			contents, err := directory.File("file1.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expectedContent, contents)
		})

		t.Run(test.name+" fresh query", func(ctx context.Context, t *testctx.T) {
			dir, directory := setup()
			entries, err := directory.Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"file1.txt"}, entries)

			require.NoError(t, os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("1"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("12"), 0o600))

			directory = c.Host().Directory(dir, test.opts)
			entries, err = directory.Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expectedEntries, entries)

			contents, err := directory.File("file1.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expectedContent, contents)
		})

		t.Run(test.name+" explicit sync always caches", func(ctx context.Context, t *testctx.T) {
			dir, directory := setup()
			directory, err := directory.Sync(ctx)
			require.NoError(t, err)

			entries, err := directory.Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"file1.txt"}, entries)

			require.NoError(t, os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("1"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("12"), 0o600))

			entries, err = directory.Entries(ctx)
			require.NoError(t, err)
			// note the expectation here doesn't vary with test.expected
			require.Equal(t, []string{"file1.txt"}, entries)

			contents, err := directory.File("file1.txt").Contents(ctx)
			require.NoError(t, err)
			// note the expectation here doesn't vary with test.expected
			require.Equal(t, "1", contents)
		})
	}
}

func findupTestDir(t *testctx.T) string {
	dir := t.TempDir()

	// /
	// ├── target.txt
	// ├── root.txt
	// └── a/
	//     ├── target.txt
	//     ├── somedir/
	//     │   └── hi.txt
	//     └── b/
	//         ├── other.txt
	//         └── c/
	//             └── leaf.txt
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), []byte("target.txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("this is root.txt"), 0o600))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "somedir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "somedir", "hi.txt"), []byte("this is a/somedir/hi.txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "target.txt"), []byte("this is a/target.txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "other.txt"), []byte("this is a/b/other.txt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "c", "leaf.txt"), []byte("leaf"), 0o600))

	return dir
}

func (HostSuite) TestFindUp(ctx context.Context, t *testctx.T) {
	dir := findupTestDir(t)
	c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(dir, "a", "b")))

	t.Run("find file in current directory", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "other.txt")
		require.NoError(t, err)
		require.Equal(t, "other.txt", found)
		content, err := c.Host().File(found).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "this is a/b/other.txt", content)
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "target.txt")
		require.NoError(t, err)
		require.Equal(t, "../target.txt", found)
		content, err := c.Host().File(found).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "this is a/target.txt", content)
	})

	t.Run("find file in root", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "root.txt")
		require.NoError(t, err)
		require.Equal(t, "../../root.txt", found)
		content, err := c.Host().File(found).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "this is root.txt", content)
	})

	t.Run("find directory in parent directory", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "somedir")
		require.NoError(t, err)
		require.Equal(t, "../somedir", found)
		entries, err := c.Host().Directory(found).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"hi.txt"}, entries)
	})

	t.Run("DO NOT find file in child directory", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "leaf.txt")
		require.NoError(t, err)
		require.Equal(t, "", found)
	})

	t.Run("DO NOT find non-existent file", func(ctx context.Context, t *testctx.T) {
		found, err := c.Host().FindUp(ctx, "nonexistent.txt")
		require.NoError(t, err)
		require.Equal(t, "", found)
	})
}

func (HostSuite) TestFile(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "d.txt"), []byte("hello world"), 0o600))

	c := connect(ctx, t)

	t.Run("get simple file", func(ctx context.Context, t *testctx.T) {
		content, err := c.Host().File(filepath.Join(dir, "a.txt")).Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "1", content)
	})

	t.Run("get nested file", func(ctx context.Context, t *testctx.T) {
		content, err := c.Host().File(filepath.Join(dir, "subdir", "d.txt")).Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello world", content)
	})
}

func (HostSuite) TestFileCacheBehavior(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	c := connect(ctx, t)

	tests := []struct {
		name     string
		opts     []dagger.HostFileOpts
		expected string
	}{
		{
			name:     "default aka cache",
			opts:     []dagger.HostFileOpts{},
			expected: "1",
		},
		{
			name:     "explicit cache",
			opts:     []dagger.HostFileOpts{{NoCache: false}},
			expected: "1",
		},
		{
			name:     "explicit no cache",
			opts:     []dagger.HostFileOpts{{NoCache: true}},
			expected: "12",
		},
	}

	for _, test := range tests {
		setup := func() (string, *dagger.File) {
			bPath := filepath.Join(dir, rand.Text())
			require.NoError(t, os.WriteFile(bPath, []byte("1"), 0o600))

			file := c.Host().File(bPath, test.opts...)
			return bPath, file
		}

		t.Run(test.name, func(ctx context.Context, t *testctx.T) {
			bPath, file := setup()
			content, err := file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "1", content)

			require.NoError(t, os.WriteFile(bPath, []byte("12"), 0o600))

			content, err = file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expected, content)
		})

		t.Run(test.name+" fresh query", func(ctx context.Context, t *testctx.T) {
			bPath, file := setup()
			content, err := file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "1", content)

			require.NoError(t, os.WriteFile(bPath, []byte("12"), 0o600))

			file = c.Host().File(bPath, test.opts...)
			content, err = file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, test.expected, content)
		})

		t.Run(test.name+" explicit sync always caches", func(ctx context.Context, t *testctx.T) {
			bPath, file := setup()
			file, err := file.Sync(ctx)
			require.NoError(t, err)

			content, err := file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "1", content)

			require.NoError(t, os.WriteFile(bPath, []byte("12"), 0o600))

			content, err = file.Contents(ctx)
			require.NoError(t, err)
			// note the expectation here doesn't vary with test.expected
			require.Equal(t, "1", content)
		})
	}
}
