package core

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type DirectorySuite struct{}

func TestDirectory(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DirectorySuite{})
}

func (DirectorySuite) TestEmpty(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			ID      core.DirectoryID
			Entries []string
		}
	}](t,
		`{
			directory {
				entries
			}
		}`, nil)
	require.NoError(t, err)
	require.Empty(t, res.Directory.Entries)
}

func (DirectorySuite) TestScratch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().Directory("/").Entries(ctx)
	require.NoError(t, err)
	// require.ErrorContains(t, err, "no such file or directory")
}

func (DirectorySuite) TestWithNewFile(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				ID      core.DirectoryID
				Entries []string
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
					entries
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.ID)
	require.Equal(t, []string{"some-file"}, res.Directory.WithNewFile.Entries)
}

func (DirectorySuite) TestEntries(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						entries
					}
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"some-file", "some-dir/"}, res.Directory.WithNewFile.WithNewFile.Entries)
}

func (DirectorySuite) TestEntriesOfPath(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						entries(path: "some-dir")
					}
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Entries)
}

func (DirectorySuite) TestDirectory(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						Entries []string
					}
				}
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						directory(path: "some-dir") {
							entries
						}
					}
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Directory.Entries)
}

func (DirectorySuite) TestDirectoryWithNewFile(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						WithNewFile struct {
							Entries []string
						}
					}
				}
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						directory(path: "some-dir") {
							withNewFile(path: "another-file", contents: "more-content") {
								entries
							}
						}
					}
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]string{"sub-file", "another-file"},
		res.Directory.WithNewFile.WithNewFile.Directory.WithNewFile.Entries)
}

func (DirectorySuite) TestWithDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		Directory("some-dir")

	entries, err := c.Directory().WithDirectory("with-dir", dir).Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "with-dir",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, entries)

	entries, err = c.Directory().WithDirectory("sub-dir/sub-sub-dir/with-dir", dir).Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "sub-dir/sub-sub-dir/with-dir",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, entries)

	t.Run("copies directory contents to .", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, entries)
	})

	t.Run("respects permissions", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("some-file", "some content", dagger.DirectoryWithNewFileOpts{Permissions: 0o444}).
			WithNewDirectory("some-dir", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o444}).
			WithNewFile("some-dir/sub-file", "sub-content", dagger.DirectoryWithNewFileOpts{Permissions: 0o444})
		ctr := c.Container().From(alpineImage).WithDirectory("/permissions-test", dir)

		stdout, err := ctr.WithExec([]string{"ls", "-ld", "/permissions-test"}).Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, stdout, "drwxr-xr-x")

		stdout, err = ctr.WithExec([]string{"ls", "-l", "/permissions-test/some-file"}).Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, stdout, "-r--r--r--")

		stdout, err = ctr.WithExec([]string{"ls", "-ld", "/permissions-test/some-dir"}).Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, stdout, "dr--r--r--")

		stdout, err = ctr.WithExec([]string{"ls", "-l", "/permissions-test/some-dir/sub-file"}).Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, stdout, "-r--r--r--")
	})
}

func (DirectorySuite) TestDirectoryFilterIncludeExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().
		WithNewFile("a.txt", "").
		WithNewFile("b.txt", "").
		WithNewFile("c.txt.rar", "").
		WithNewFile("subdir/d.txt", "").
		WithNewFile("subdir/e.txt", "").
		WithNewFile("subdir/f.txt.rar", "")

	t.Run("exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := dir.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "subdir/"}, entries)
	})

	t.Run("include", func(ctx context.Context, t *testctx.T) {
		entries, err := dir.Filter(dagger.DirectoryFilterOpts{
			Include: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar"}, entries)
	})

	t.Run("exclude overrides include", func(ctx context.Context, t *testctx.T) {
		entries, err := dir.Filter(dagger.DirectoryFilterOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt"}, entries)
	})

	t.Run("include does not override exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := dir.Filter(dagger.DirectoryFilterOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{}, entries)
	})

	t.Run("exclude works on directory", func(ctx context.Context, t *testctx.T) {
		entries, err := dir.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{"subdir"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "c.txt.rar"}, entries)
	})

	t.Run("exclude respects subdir", func(ctx context.Context, t *testctx.T) {
		subdir := dir.Directory("subdir")
		entries, err := subdir.Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"d.txt", "e.txt"}, entries)
	})
}

func (DirectorySuite) TestWithDirectoryIncludeExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().
		WithNewFile("a.txt", "").
		WithNewFile("b.txt", "").
		WithNewFile("c.txt.rar", "").
		WithNewFile("subdir/d.txt", "").
		WithNewFile("subdir/e.txt", "").
		WithNewFile("subdir/f.txt.rar", "")

	t.Run("exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "subdir/"}, entries)
	})

	t.Run("include", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar"}, entries)
	})

	t.Run("exclude overrides include", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt"}, entries)
	})

	t.Run("include does not override exclude", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{}, entries)
	})

	t.Run("exclude works on directory", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"subdir"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "c.txt.rar"}, entries)
	})

	subdir := dir.Directory("subdir")

	t.Run("exclude respects subdir", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithDirectory(".", subdir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"d.txt", "e.txt"}, entries)
	})
}

func (DirectorySuite) TestWithNewDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().
		WithNewDirectory("a").
		WithNewDirectory("b/c")

	entries, err := dir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"a/", "b/"}, entries)

	entries, err = dir.Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "b",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"c/"}, entries)

	t.Run("does not permit creating directory outside of root", func(ctx context.Context, t *testctx.T) {
		_, err := dir.Directory("b").WithNewDirectory("../c").Sync(ctx)
		require.ErrorContains(t, err, "cannot create directory outside parent")
	})
}

func (DirectorySuite) TestWithFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-other-file", "some-other-content").
		File("some-file")

	dirWithFile := c.Directory().WithFile("target-file", file)
	content, err := dirWithFile.
		File("target-file").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", content)
	_, err = dirWithFile.File("some-other-file").Contents(ctx)
	require.Error(t, err)

	// Same as above, but use the same name for the file rather than changing it.
	// Needed for testing merge-op corner cases.
	dirWithFile = c.Directory().WithFile("some-file", file)
	content, err = dirWithFile.
		File("some-file").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", content)
	_, err = dirWithFile.File("some-other-file").Contents(ctx)
	require.Error(t, err)

	content, err = c.Directory().
		WithFile("sub-dir/target-file", file).
		File("sub-dir/target-file").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", content)

	t.Run("respects permissions", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile(
				"file-with-permissions",
				"this should have rwxrwxrwx permissions",
				dagger.DirectoryWithNewFileOpts{Permissions: 0o777})

		ctr := c.Container().From(alpineImage).WithDirectory("/permissions-test", dir)

		stdout, err := ctr.WithExec([]string{"ls", "-l", "/permissions-test/file-with-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "rwxrwxrwx")

		dir2 := c.Directory().
			WithNewFile(
				"file-with-permissions",
				"this should have rw-r--r-- permissions")
		ctr2 := c.Container().From(alpineImage).WithDirectory("/permissions-test", dir2)
		stdout2, err := ctr2.WithExec([]string{"ls", "-l", "/permissions-test/file-with-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout2, "rw-r--r--")
	})

	t.Run("dir reference is kept", func(ctx context.Context, t *testctx.T) {
		f := c.Directory().WithNewFile("some-file", "data").File("some-file")

		d2 := c.Directory().
			WithNewFile("some-other-file", "other-data").
			WithNewDirectory("some-dir").
			Directory("/some-dir").
			WithFile("f", f)

		// this should no longer be available, since dir.Dir should now be "/dir1"
		_, err := d2.File("some-other-file").Contents(ctx)
		require.Error(t, err)

		s, err := d2.File("f").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	for _, dst := range []string{".", "", "/"} {
		t.Run(fmt.Sprintf("src filename is used dst is a directory referenced by %s", dst), func(ctx context.Context, t *testctx.T) {
			f := c.Directory().WithNewFile("some-file", "data").File("some-file")
			d := c.Directory().WithFile(dst, f)
			s, err := d.File("some-file").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "data", s)
		})
	}

	t.Run("src filename (and not directory names) is used dst is empty", func(ctx context.Context, t *testctx.T) {
		f := c.Directory().WithNewFile("sub/subterrain/some-file", "data").File("sub/subterrain/some-file")
		d := c.Directory().WithFile("", f)
		s, err := d.File("some-file").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})
}

func (DirectorySuite) TestWithFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file1 := c.Directory().
		WithNewFile("first-file", "file1 content").
		File("first-file")
	file2 := c.Directory().
		WithNewFile("second-file", "file2 content").
		File("second-file")
	files := []*dagger.File{file1, file2}

	check := func(ctx context.Context, t *testctx.T, dir *dagger.Directory, path string) {
		contents, err := dir.File(filepath.Join(path, "first-file")).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file1 content", contents)

		contents, err = dir.File(filepath.Join(path, "second-file")).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "file2 content", contents)
	}

	t.Run("root", func(ctx context.Context, t *testctx.T) {
		path := "/"
		dir := c.Directory().WithFiles(path, files)
		check(ctx, t, dir, path)
	})

	t.Run("sub", func(ctx context.Context, t *testctx.T) {
		path := "/a/b/c"
		dir := c.Directory().WithFiles(path, files)
		check(ctx, t, dir, path)
	})

	t.Run("sub trailing", func(ctx context.Context, t *testctx.T) {
		path := "/a/b/c/"
		dir := c.Directory().WithFiles(path, files)
		check(ctx, t, dir, path)
	})

	t.Run("respects permissions", func(ctx context.Context, t *testctx.T) {
		file1 := c.Directory().
			WithNewFile("file-set-permissions", "this should have rwxrwxrwx permissions", dagger.DirectoryWithNewFileOpts{Permissions: 0o777}).
			File("file-set-permissions")
		file2 := c.Directory().
			WithNewFile("file-default-permissions", "this should have rw-r--r-- permissions").
			File("file-default-permissions")
		files := []*dagger.File{file1, file2}
		dir := c.Directory().
			WithFiles("/", files)

		ctr := c.Container().From(alpineImage).WithDirectory("/permissions-test", dir)

		stdout, err := ctr.WithExec([]string{"ls", "-l", "/permissions-test/file-set-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "rwxrwxrwx")

		ctr2 := c.Container().From(alpineImage).WithDirectory("/permissions-test", dir)
		stdout2, err := ctr2.WithExec([]string{"ls", "-l", "/permissions-test/file-default-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout2, "rw-r--r--")
	})
}

func (DirectorySuite) TestWithTimestamps(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	reallyImportantTime := time.Date(1985, 10, 26, 8, 15, 0, 0, time.UTC)

	dir := c.Container().
		From(alpineImage).
		WithExec([]string{"sh", "-c", `
		  mkdir output
			touch output/some-file
			mkdir output/sub-dir
			touch output/sub-dir/sub-file
		`}).
		Directory("output").
		WithTimestamps(int(reallyImportantTime.Unix()))

	t.Run("changes file and directory timestamps recursively", func(ctx context.Context, t *testctx.T) {
		ls, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/dir", dir).
			WithEnvVariable("RANDOM", identity.NewID()).
			WithExec([]string{"sh", "-c", "ls -al /dir && ls -al /dir/sub-dir"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+\d+ root\s+root\s+\d+ Oct 26  1985 some-file`), ls)
		require.Regexp(t, regexp.MustCompile(`drwxr-xr-x\s+\d+ root\s+root\s+\d+ Oct 26  1985 sub-dir`), ls)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+\d+ root\s+root\s+\d+ Oct 26  1985 sub-file`), ls)
	})

	t.Run("results in stable tar archiving", func(ctx context.Context, t *testctx.T) {
		content, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/dir", dir).
			WithEnvVariable("RANDOM", identity.NewID()).
			// NB: there's a gotcha here: we need to tar * and not . because the
			// directory itself has an unstable timestamp. :(
			WithExec([]string{"sh", "-c", "tar -cf - -C /dir * | sha256sum -"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "5f70bf18a086007016e948b04aed3b82103a36bea41755b6cddfaf10ace3c6ef")
	})
}

func (DirectorySuite) TestWithoutPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir1 := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content")

	entries, err := dir1.
		WithoutDirectory("some-dir").
		Entries(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"some-file"}, entries)

	entries, err = dir1.
		WithoutDirectory("non-existent").
		Entries(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"some-dir/", "some-file"}, entries)

	dir := c.Directory().
		WithNewFile("foo.txt", "foo").
		WithNewFile("a/bar.txt", "bar").
		WithNewFile("a/data.json", "{\"datum\": 10}").
		WithNewFile("b/foo.txt", "foo").
		WithNewFile("b/bar.txt", "bar").
		WithNewFile("b/data.json", "{\"datum\": 10}").
		WithNewFile("c/file-a1.txt", "file-a1.txt").
		WithNewFile("c/file-a1.json", "file-a1.json").
		WithNewFile("c/file-b1.txt", "file-b1.txt").
		WithNewFile("c/file-b1.json", "file-b1.json")

	entries, err = dir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"a/", "b/", "c/", "foo.txt"}, entries)

	entries, err = dir.
		WithoutDirectory("a").
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"b/", "c/", "foo.txt"}, entries)

	entries, err = dir.
		WithoutFile("b/*.txt").
		Entries(ctx, dagger.DirectoryEntriesOpts{Path: "b"})

	require.NoError(t, err)
	require.Equal(t, []string{"data.json"}, entries)

	entries, err = dir.
		WithoutFile("c/*a1*").
		Entries(ctx, dagger.DirectoryEntriesOpts{Path: "c"})

	require.NoError(t, err)
	require.Equal(t, []string{"file-b1.json", "file-b1.txt"}, entries)

	dirDir := c.Directory().
		WithNewFile("foo.txt", "foo").
		WithNewFile("a1/a1-file", "a1-file").
		WithNewFile("a2/a2-file", "a2-file").
		WithNewFile("b1/b1-file", "b1-file")

	entries, err = dirDir.WithoutDirectory("a*").Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"b1/", "foo.txt"}, entries)

	// Test WithoutFile
	filesDir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		WithoutFile("some-file")

	entries, err = filesDir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"some-dir/"}, entries)

	// Test WithoutFiles
	filesDir = c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-file-2", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		WithoutFiles([]string{"some-file", "some-file-2"})

	entries, err = filesDir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"some-dir/"}, entries)

	// verify WithoutFile works when dir has be selected to a subdir
	subdirWithout := c.Directory().
		WithDirectory("subdir", c.Directory().
			WithNewFile("some-file", "delete me").
			WithNewFile("some-other-file", "keep me"),
		).
		Directory("subdir").
		WithoutFile("some-file")
	entries, err = subdirWithout.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"some-other-file"}, entries)
}

func (DirectorySuite) TestDiff(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		aID := newDirWithFile(t, "a-file", "a-content")
		bID := newDirWithFile(t, "b-file", "b-content")

		diff := `query Diff($id: DirectoryID!, $other: DirectoryID!) {
			loadDirectoryFromID(id: $id) {
				diff(other: $other) {
					entries
				}
			}
		}`
		c := connect(ctx, t)
		res, err := testutil.QueryWithClient[struct {
			Directory struct {
				Diff struct {
					Entries []string
				}
			} `json:"loadDirectoryFromID"`
		}](c, t, diff, &testutil.QueryOptions{
			Variables: map[string]any{
				"id":    aID,
				"other": bID,
			},
		})
		require.NoError(t, err)

		require.Equal(t, []string{"b-file"}, res.Directory.Diff.Entries)

		res, err = testutil.QueryWithClient[struct {
			Directory struct {
				Diff struct {
					Entries []string
				}
			} `json:"loadDirectoryFromID"`
		}](c, t, diff, &testutil.QueryOptions{
			Variables: map[string]any{
				"id":    bID,
				"other": aID,
			},
		})
		require.NoError(t, err)

		require.Equal(t, []string{"a-file"}, res.Directory.Diff.Entries)
	})

	// this is a regression test for: https://github.com/dagger/dagger/pull/7328
	t.Run("equivalent subdirs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		a := c.Git("github.com/dagger/dagger").Ref("main").Tree()
		b := c.Directory().WithDirectory("", a)
		ents, err := a.Diff(b).Entries(ctx)
		require.NoError(t, err)
		require.Len(t, ents, 0)
	})

	/*
		This triggers a nil panic in Buildkit!

		Issue: https://github.com/dagger/dagger/issues/3337

		This might be fixed once we update Buildkit.

		err = testutil.Query(diff, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"id":    aID,
				"other": aID,
			},
		})
		require.NoError(t, err)

		require.Empty(t, res.Directory.Diff.Entries)
	*/
}

func (DirectorySuite) TestChanges(ctx context.Context, t *testctx.T) {
	t.Run("removedPaths basic", func(ctx context.Context, t *testctx.T) {
		// Create a directory with files
		c := connect(ctx, t)

		// Create initial directory with multiple files
		oldDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2").
			WithNewFile("removed.txt", "to be removed")

		// Create new directory without one of the files
		newDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		require.Contains(t, removedPaths, "removed.txt")
		require.NotContains(t, removedPaths, "file1.txt")
		require.NotContains(t, removedPaths, "dir/file2.txt")
	})

	t.Run("removedPaths with directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with subdirectories and nested files
		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove-dir/file.txt", "remove").
			WithNewFile("remove-dir/subdir/nested.txt", "nested").
			WithNewDirectory("empty-dir")

		// Create new directory without the subdirectories
		newDir := c.Directory().
			WithNewFile("keep.txt", "keep")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		// Should include the directories with trailing slash
		require.Contains(t, removedPaths, "remove-dir/")
		require.Contains(t, removedPaths, "empty-dir/")

		// Should NOT include individual files in the removed directory
		require.NotContains(t, removedPaths, "remove-dir/file.txt")
		require.NotContains(t, removedPaths, "remove-dir/subdir/")
		require.NotContains(t, removedPaths, "remove-dir/subdir/nested.txt")

		// Should not include files that weren't removed
		require.NotContains(t, removedPaths, "keep.txt")
	})

	t.Run("removedPaths mixed files and directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with mix of files and directories
		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove-file.txt", "remove me").
			WithNewFile("remove-dir/file.txt", "in dir").
			WithNewFile("keep-dir/file.txt", "keep dir")

		// Create new directory keeping some files and directories
		newDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("keep-dir/file.txt", "keep dir")

		changes := newDir.Changes(oldDir)

		removedPaths, err := changes.RemovedPaths(ctx)
		require.NoError(t, err)

		// Should include individual removed files
		require.Contains(t, removedPaths, "remove-file.txt")

		// Should include removed directory but not its contents
		require.Contains(t, removedPaths, "remove-dir/")
		require.NotContains(t, removedPaths, "remove-dir/file.txt")

		// Should not include kept items
		require.NotContains(t, removedPaths, "keep.txt")
		require.NotContains(t, removedPaths, "keep-dir/")
		require.NotContains(t, removedPaths, "keep-dir/file.txt")
	})

	t.Run("addedFiles basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with some files
		oldDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2")

		// Create new directory with additional files
		newDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2").
			WithNewFile("added.txt", "new content").
			WithNewFile("dir/added2.txt", "new content2").
			WithNewFile("new-dir/added3.txt", "new content3")

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)

		// Should include added files
		require.Contains(t, addedFiles, "added.txt")
		require.Contains(t, addedFiles, "dir/added2.txt")
		require.Contains(t, addedFiles, "new-dir/added3.txt")

		// Should not include existing files
		require.NotContains(t, addedFiles, "existing.txt")
		require.NotContains(t, addedFiles, "dir/existing2.txt")
	})

	t.Run("addedFiles excludes directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("old-dir/file.txt", "new")

		newDir := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("old-dir/new-file.txt", "new").
			WithNewFile("new-dir/file.txt", "new").
			WithNewDirectory("empty-dir")

		changes := newDir.Changes(oldDir)

		addedFiles, err := changes.AddedPaths(ctx)
		require.NoError(t, err)

		// Should include added files only
		require.Contains(t, addedFiles, "new-dir/file.txt")

		// Should only include NEW directories
		require.NotContains(t, addedFiles, "old-dir/")
		require.Contains(t, addedFiles, "new-dir/")
		require.Contains(t, addedFiles, "empty-dir/")

		// Should not include existing files
		require.NotContains(t, addedFiles, "keep.txt")
	})

	t.Run("modifiedPaths basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory
		oldDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "original content").
			WithNewFile("dir/changed2.txt", "original content2").
			WithNewFile("will-be-removed.txt", "remove me")

		// Create new directory with changes
		newDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "modified content").
			WithNewFile("dir/changed2.txt", "modified content2").
			WithNewFile("added.txt", "new file")

		changes := newDir.Changes(oldDir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should include changed files
		require.Contains(t, modifiedPaths, "changed.txt")
		require.Contains(t, modifiedPaths, "dir/changed2.txt")

		// Should not include unchanged files
		require.NotContains(t, modifiedPaths, "unchanged.txt")

		// Should not include added files
		require.NotContains(t, modifiedPaths, "added.txt")

		// Should not include removed files
		require.NotContains(t, modifiedPaths, "will-be-removed.txt")
	})

	t.Run("modifiedPaths with empty changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create identical directories
		dir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := dir.Changes(dir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should be empty when no changes
		require.Empty(t, modifiedPaths)
	})

	t.Run("modifiedPaths excludes directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		oldDir := c.Directory().
			WithNewFile("dir/file.txt", "old content")

		newDir := c.Directory().
			WithNewFile("dir/file.txt", "new content").
			WithNewFile("dir/added.txt", "added content")

		changes := newDir.Changes(oldDir)

		modifiedPaths, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)

		// Should include changed files only
		require.Contains(t, modifiedPaths, "dir/file.txt")

		// Should NOT include directories or added files
		require.NotContains(t, modifiedPaths, "dir/")
		require.NotContains(t, modifiedPaths, "dir/added.txt")
	})

	t.Run("layer basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory
		oldDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "original content").
			WithNewFile("dir/changed2.txt", "original content2").
			WithNewFile("will-be-removed.txt", "remove me")

		// Create new directory with changes
		newDir := c.Directory().
			WithNewFile("unchanged.txt", "same content").
			WithNewFile("changed.txt", "modified content").
			WithNewFile("dir/changed2.txt", "modified content2").
			WithNewFile("added.txt", "new file")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		// Verify layer contains modified files
		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "changed.txt")
		require.Contains(t, entries, "dir/")
		require.Contains(t, entries, "added.txt")

		// Verify layer excludes unchanged and removed files
		require.NotContains(t, entries, "unchanged.txt")
		require.NotContains(t, entries, "will-be-removed.txt")

		// Verify file contents in layer
		changedContent, err := layer.File("changed.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified content", changedContent)

		addedContent, err := layer.File("added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new file", addedContent)

		// Verify nested file in layer
		dirEntries, err := layer.Directory("dir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEntries, "changed2.txt")

		changed2Content, err := layer.File("dir/changed2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified content2", changed2Content)
	})

	t.Run("layer with only added files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with some files
		oldDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2")

		// Create new directory with additional files (no modifications)
		newDir := c.Directory().
			WithNewFile("existing.txt", "content1").
			WithNewFile("dir/existing2.txt", "content2").
			WithNewFile("added.txt", "new content").
			WithNewFile("dir/added2.txt", "new content2").
			WithNewFile("new-dir/added3.txt", "new content3")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include added files
		require.Contains(t, entries, "added.txt")
		require.Contains(t, entries, "dir/")
		require.Contains(t, entries, "new-dir/")

		// Should not include existing files
		require.NotContains(t, entries, "existing.txt")

		// Verify added files have correct content
		addedContent, err := layer.File("added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content", addedContent)

		added2Content, err := layer.File("dir/added2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content2", added2Content)

		added3Content, err := layer.File("new-dir/added3.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content3", added3Content)
	})

	t.Run("layer excludes removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with files to be removed and modified
		oldDir := c.Directory().
			WithNewFile("keep-and-change.txt", "original").
			WithNewFile("remove-me.txt", "will be removed").
			WithNewFile("remove-dir/file.txt", "in removed dir")

		// Create new directory without removed files but with changes
		newDir := c.Directory().
			WithNewFile("keep-and-change.txt", "modified").
			WithNewFile("new-file.txt", "newly added")

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include modified and added files
		require.Contains(t, entries, "keep-and-change.txt")
		require.Contains(t, entries, "new-file.txt")

		// Should NOT include removed files or directories
		require.NotContains(t, entries, "remove-me.txt")
		require.NotContains(t, entries, "remove-dir/")

		// Verify modified file has new content
		modifiedContent, err := layer.File("keep-and-change.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", modifiedContent)
	})

	t.Run("layer with empty changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create identical directories
		dir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		changes := dir.Changes(dir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should be empty when no changes
		require.Empty(t, entries)
	})

	t.Run("layer with nested directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create initial directory with nested structure
		oldDir := c.Directory().
			WithNewFile("root.txt", "root content").
			WithNewFile("level1/file.txt", "level1 original").
			WithNewFile("level1/level2/file.txt", "level2 original").
			WithNewFile("level1/level2/level3/deep.txt", "deep original")

		// Create new directory with changes at various levels
		newDir := c.Directory().
			WithNewFile("root.txt", "root content").                       // unchanged
			WithNewFile("level1/file.txt", "level1 modified").             // changed
			WithNewFile("level1/level2/file.txt", "level2 original").      // unchanged
			WithNewFile("level1/level2/level3/deep.txt", "deep modified"). // changed
			WithNewFile("level1/level2/level3/added.txt", "newly added").  // added
			WithNewFile("level1/added-level2/new.txt", "added in new dir") // added in new dir

		changes := newDir.Changes(oldDir)
		layer := changes.Layer()

		entries, err := layer.Entries(ctx)
		require.NoError(t, err)

		// Should include directories with changes
		require.Contains(t, entries, "level1/")

		// Verify nested structure is preserved
		level1Entries, err := layer.Directory("level1").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level1Entries, "file.txt")
		require.Contains(t, level1Entries, "level2/")
		require.Contains(t, level1Entries, "added-level2/")

		level2Entries, err := layer.Directory("level1/level2").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level2Entries, "level3/")

		level3Entries, err := layer.Directory("level1/level2/level3").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, level3Entries, "deep.txt")
		require.Contains(t, level3Entries, "added.txt")

		// Verify file contents
		modifiedContent, err := layer.File("level1/file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "level1 modified", modifiedContent)

		deepContent, err := layer.File("level1/level2/level3/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep modified", deepContent)

		addedContent, err := layer.File("level1/level2/level3/added.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newly added", addedContent)

		newDirContent, err := layer.File("level1/added-level2/new.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "added in new dir", newDirContent)

		// Verify root.txt is NOT included (unchanged)
		require.NotContains(t, entries, "root.txt")
	})
}

func (s DirectorySuite) TestWithChanges(ctx context.Context, t *testctx.T) {
	s.testChangeApplying(t, func(dest *dagger.Directory, source *dagger.Changeset) *dagger.Directory {
		return dest.WithChanges(source)
	}, false)
}

func (s DirectorySuite) TestChangesAsPatch(ctx context.Context, t *testctx.T) {
	s.testChangeApplying(t, func(dest *dagger.Directory, source *dagger.Changeset) *dagger.Directory {
		return dest.WithPatchFile(source.AsPatch())
	}, true)
}

func (DirectorySuite) testChangeApplying(t *testctx.T, apply func(*dagger.Directory, *dagger.Changeset) *dagger.Directory, leaveDirs bool) {
	t.Run("basic usage with added, changed, and removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("keep.txt", "unchanged").
			WithNewFile("change.txt", "original").
			WithNewFile("remove.txt", "will be removed").
			WithNewFile("subdir/nested.txt", "nested original")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with changes
		afterDir := c.Directory().
			WithNewFile("keep.txt", "unchanged").           // unchanged
			WithNewFile("change.txt", "modified").          // changed
			WithNewFile("add.txt", "newly added").          // added
			WithNewFile("subdir/nested.txt", "nested mod"). // changed in subdir
			WithNewFile("subdir/new.txt", "new in subdir")  // added in subdir
		// Note: remove.txt is not included (removed)

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		// Should have: keep.txt, change.txt (modified), add.txt (new), subdir/ (with changes)
		// Should NOT have: remove.txt (removed)
		require.Contains(t, entries, "keep.txt")
		require.Contains(t, entries, "change.txt")
		require.Contains(t, entries, "add.txt")
		require.Contains(t, entries, "subdir/")
		require.NotContains(t, entries, "remove.txt")

		// Verify file contents
		keepContent, err := resultDir.File("keep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "unchanged", keepContent)

		changeContent, err := resultDir.File("change.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", changeContent)

		addContent, err := resultDir.File("add.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newly added", addContent)

		// Verify subdirectory entries
		subdirEntries, err := resultDir.Directory("subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, subdirEntries, "nested.txt")
		require.Contains(t, subdirEntries, "new.txt")

		// Verify subdirectory file contents
		nestedContent, err := resultDir.File("subdir/nested.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "nested mod", nestedContent)

		newInSubdirContent, err := resultDir.File("subdir/new.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new in subdir", newInSubdirContent)
	})

	t.Run("only added files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with some files
		baseDir := c.Directory().
			WithNewFile("existing.txt", "existing content")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with additional files
		afterDir := baseDir.
			WithNewFile("new1.txt", "new content 1").
			WithNewFile("dir/new2.txt", "new content 2")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "existing.txt")
		require.Contains(t, entries, "new1.txt")
		require.Contains(t, entries, "dir/")

		// Verify content
		existingContent, err := resultDir.File("existing.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "existing content", existingContent)

		new1Content, err := resultDir.File("new1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content 1", new1Content)

		new2Content, err := resultDir.File("dir/new2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content 2", new2Content)
	})

	t.Run("only changed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("file1.txt", "original 1").
			WithNewFile("dir/file2.txt", "original 2")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with modifications
		afterDir := c.Directory().
			WithNewFile("file1.txt", "modified 1").
			WithNewFile("dir/file2.txt", "modified 2")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		file1Content, err := resultDir.File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified 1", file1Content)

		file2Content, err := resultDir.File("dir/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified 2", file2Content)
	})

	t.Run("only removed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("keep.txt", "keep this").
			WithNewFile("remove1.txt", "remove this").
			WithNewFile("dir/remove2.txt", "remove this too")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory with files removed
		afterDir := c.Directory().
			WithNewFile("keep.txt", "keep this")
		// Note: remove1.txt and dir/remove2.txt are not included

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "keep.txt")
		require.NotContains(t, entries, "remove1.txt")
		if !leaveDirs {
			require.NotContains(t, entries, "dir/")
		}

		// Verify content of kept file
		keepContent, err := resultDir.File("keep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep this", keepContent)
	})

	t.Run("no changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory
		baseDir := c.Directory().
			WithNewFile("file1.txt", "content1").
			WithNewFile("dir/file2.txt", "content2")

		// Create identical before and after directories
		beforeDir := baseDir
		afterDir := baseDir

		// Create changes (should be empty)
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result is identical to the original
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		originalEntries, err := baseDir.Entries(ctx)
		require.NoError(t, err)

		require.ElementsMatch(t, originalEntries, entries)

		// Verify file contents are unchanged
		file1Content, err := resultDir.File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content1", file1Content)

		file2Content, err := resultDir.File("dir/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content2", file2Content)
	})

	t.Run("applying changes to different base directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create before directory
		beforeDir := c.Directory().
			WithNewFile("common.txt", "before").
			WithNewFile("only-in-before.txt", "before only")

		// Create after directory with changes
		afterDir := c.Directory().
			WithNewFile("common.txt", "after").
			WithNewFile("only-in-after.txt", "after only")
		// Note: only-in-before.txt is removed

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to a different base directory
		differentBaseDir := c.Directory().
			WithNewFile("common.txt", "base version").
			WithNewFile("only-in-before.txt", "base has this too").
			WithNewFile("base-specific.txt", "only in base")

		resultDir := differentBaseDir.WithChanges(changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "common.txt")
		require.Contains(t, entries, "only-in-after.txt")
		require.Contains(t, entries, "base-specific.txt")
		require.NotContains(t, entries, "only-in-before.txt") // Should be removed

		// Verify contents
		commonContent, err := resultDir.File("common.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "after", commonContent) // Should be the "after" version

		afterOnlyContent, err := resultDir.File("only-in-after.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "after only", afterOnlyContent)

		baseSpecificContent, err := resultDir.File("base-specific.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "only in base", baseSpecificContent) // Should be preserved
	})

	t.Run("complex nested structure changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create complex base directory
		baseDir := c.Directory().
			WithNewFile("root.txt", "root content").
			WithNewFile("level1/file1.txt", "level1 content").
			WithNewFile("level1/level2/file2.txt", "level2 content").
			WithNewFile("level1/level2/level3/file3.txt", "level3 content").
			WithNewFile("another/path/file.txt", "another content")

		beforeDir := baseDir

		// Create after directory with complex changes
		afterDir := baseDir.
			WithNewFile("root.txt", "modified root").              // changed
			WithNewFile("level1/level2/file2.txt", "modified l2"). // changed
			WithNewFile("level1/level2/newfile.txt", "new file").  // added
			WithNewFile("new/deep/path/newfile.txt", "deep new").  // added deep
			WithNewFile("another/different.txt", "different")      // added

		changes := afterDir.Changes(beforeDir)
		resultDir := apply(baseDir, changes)

		// Verify structure
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "root.txt")
		require.Contains(t, entries, "level1/")
		require.Contains(t, entries, "new/")
		require.Contains(t, entries, "another/")

		// Verify changed content
		rootContent, err := resultDir.File("root.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified root", rootContent)

		l2Content, err := resultDir.File("level1/level2/file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified l2", l2Content)

		// Verify added files
		newFileContent, err := resultDir.File("level1/level2/newfile.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new file", newFileContent)

		deepNewContent, err := resultDir.File("new/deep/path/newfile.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep new", deepNewContent)

		// Verify another/different.txt was added
		differentContent, err := resultDir.File("another/different.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "different", differentContent)
	})

	if leaveDirs {
		return
	}

	t.Run("removed entire directories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with nested structure
		baseDir := c.Directory().
			WithNewFile("keep.txt", "keep this").
			WithNewFile("removedir/file1.txt", "remove me").
			WithNewFile("removedir/subdir/file2.txt", "remove me too").
			WithNewDirectory("emptydir")

		// Create before directory (same as base)
		beforeDir := baseDir

		// Create after directory without the directories
		afterDir := c.Directory().
			WithNewFile("keep.txt", "keep this")

		// Create changes
		changes := afterDir.Changes(beforeDir)

		// Apply changes to the base directory
		resultDir := apply(baseDir, changes)

		// Verify the result
		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "keep.txt")
		require.NotContains(t, entries, "removedir/")
		require.NotContains(t, entries, "emptydir/")

		// Verify we can't access removed files
		_, err = resultDir.File("removedir/file1.txt").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("empty directories handling", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Create base directory with empty directories
		baseDir := c.Directory().
			WithNewFile("file.txt", "content").
			WithNewDirectory("empty1").
			WithNewDirectory("empty2")

		beforeDir := baseDir

		// Create after directory removing one empty dir and adding another
		afterDir := c.Directory().
			WithNewFile("file.txt", "content").
			WithNewDirectory("empty2").
			WithNewDirectory("new-empty")

		changes := afterDir.Changes(beforeDir)
		resultDir := apply(baseDir, changes)

		entries, err := resultDir.Entries(ctx)
		require.NoError(t, err)

		require.Contains(t, entries, "file.txt")
		require.Contains(t, entries, "empty2/")
		require.Contains(t, entries, "new-empty/")
		require.NotContains(t, entries, "empty1/")

		// Verify directories are actually directories
		exists, err := resultDir.Directory("empty2").Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, exists) // Should be empty

		exists2, err := resultDir.Directory("new-empty").Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, exists2) // Should be empty
	})
}

func (DirectorySuite) TestExport(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	dest := t.TempDir()

	c := connect(ctx, t, dagger.WithWorkdir(wd))

	dir := c.Container().From(alpineImage).Directory("/etc/profile.d")

	t.Run("to absolute dir", func(ctx context.Context, t *testctx.T) {
		actual, err := dir.Export(ctx, dest)
		require.NoError(t, err)
		require.Equal(t, dest, actual)

		entries, err := ls(dest)
		require.NoError(t, err)
		require.Equal(t, []string{"20locale.sh", "README", "color_prompt.sh.disabled"}, entries)
	})

	t.Run("to workdir", func(ctx context.Context, t *testctx.T) {
		actual, err := dir.Export(ctx, ".")
		require.NoError(t, err)
		require.Equal(t, wd, actual)

		entries, err := ls(wd)
		require.NoError(t, err)
		require.Equal(t, []string{"20locale.sh", "README", "color_prompt.sh.disabled"}, entries)

		t.Run("wipe flag", func(ctx context.Context, t *testctx.T) {
			dir := dir.WithoutFile("README")

			// by default a delete in the source dir won't overwrite the destination on the host
			actual, err := dir.Export(ctx, ".")
			require.NoError(t, err)
			require.Contains(t, wd, actual)
			entries, err = ls(wd)
			require.NoError(t, err)
			require.Equal(t, []string{"20locale.sh", "README", "color_prompt.sh.disabled"}, entries)

			// wipe results in the destination being replaced with the source entirely, including deletes
			actual, err = dir.Export(ctx, ".", dagger.DirectoryExportOpts{Wipe: true})
			require.NoError(t, err)
			require.Equal(t, wd, actual)
			entries, err = ls(wd)
			require.NoError(t, err)
			require.Equal(t, []string{"20locale.sh", "color_prompt.sh.disabled"}, entries)
		})
	})

	t.Run("to outer dir", func(ctx context.Context, t *testctx.T) {
		actual, err := dir.Export(ctx, "../")
		require.NoError(t, err)
		require.Contains(t, actual, "/")
	})
}

func (DirectorySuite) TestWithNewFileExceedingLength(ctx context.Context, t *testctx.T) {
	_, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt", contents: "some-content") {
					sync
				}
			}
		}`, nil)
	require.Error(t, err)
	requireErrOut(t, err, "File name length exceeds the maximum supported 255 characters")
}

func (DirectorySuite) TestWithFileExceedingLength(ctx context.Context, t *testctx.T) {
	_, err := testutil.Query[struct {
		Directory struct {
			WithNewFile struct {
				file struct {
					ID core.DirectoryID
				}
			}
		}
	}](t,
		`{
			directory {
				withNewFile(path: "dir/bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt", contents: "some-content") {
					file(path: "dir/bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt") {
						id
					}
				}
			}
		}`, nil)
	require.Error(t, err)
	requireErrOut(t, err, "File name length exceeds the maximum supported 255 characters")
}

func (DirectorySuite) TestDirectMerge(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	getDirAndInodes := func(t *testctx.T, fileNames ...string) (*dagger.Directory, []string) {
		t.Helper()
		ctr := c.Container().From(alpineImage).
			WithMountedDirectory("/src", c.Directory()).
			WithWorkdir("/src")

		var inodes []string
		for _, fileName := range fileNames {
			ctr = ctr.WithExec([]string{
				"sh", "-e", "-x", "-c",
				"touch " + fileName + " && stat -c '%i' " + fileName,
			})
			out, err := ctr.Stdout(ctx)
			require.NoError(t, err)
			inodes = append(inodes, strings.TrimSpace(out))
		}
		return ctr.Directory("/src"), inodes
	}

	// verify optimized hardlink based merge-op is used by verifying inodes are preserved
	// across WithDirectory calls
	mergeDir := c.Directory()
	fileGroups := [][]string{
		{"abc", "xyz"},
		{"123", "456", "789"},
		{"foo"},
		{"bar"},
	}
	fileNameToInode := map[string]string{}
	for _, fileNames := range fileGroups {
		newDir, inodes := getDirAndInodes(t, fileNames...)
		for i, fileName := range fileNames {
			fileNameToInode[fileName] = inodes[i]
		}
		mergeDir = mergeDir.WithDirectory("/", newDir)
	}

	ctr := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", mergeDir).
		WithWorkdir("/mnt")

	for fileName, inode := range fileNameToInode {
		out, err := ctr.WithExec([]string{"stat", "-c", "%i", fileName}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, inode, strings.TrimSpace(out), "file %s should have inode %s", fileName, inode)
	}
}

func (DirectorySuite) TestFallbackMerge(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("dest path same as src selector", func(ctx context.Context, t *testctx.T) {
		// corner case where we need to use the fallback rather than direct merge
		srcDir := c.Directory().
			WithNewFile("/toplevel", "").
			WithNewFile("/dir/lowerlevel", "")
		srcSubdir := srcDir.Directory("/dir")

		mergedDir := c.Directory().WithDirectory("/dir", srcSubdir)
		_, err := mergedDir.File("/dir/lowerlevel").Contents(ctx)
		require.NoError(t, err)
		_, err = mergedDir.File("/toplevel").Contents(ctx)
		require.Error(t, err)
	})
}

func (DirectorySuite) TestSync(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("empty", func(ctx context.Context, t *testctx.T) {
		dir, err := c.Directory().Sync(ctx)
		require.NoError(t, err)

		entries, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("triggers error", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().Directory("/foo").Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "no such file or directory")

		_, err = c.Container().From(alpineImage).Directory("/bar").Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "no such file or directory")
	})

	t.Run("allows chaining", func(ctx context.Context, t *testctx.T) {
		dir, err := c.Directory().WithNewFile("foo", "bar").Sync(ctx)
		require.NoError(t, err)

		entries, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"foo"}, entries)
	})
}

func (DirectorySuite) TestGlob(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcDir := c.Directory().
		WithNewFile("main.go", "").
		WithNewFile("func.go", "").
		WithNewFile("test.md", "").
		WithNewFile("foo.txt", "").
		WithNewFile("README.txt", "").
		WithNewDirectory("/subdir").
		WithNewFile("/subdir/foo.txt", "").
		WithNewFile("/subdir/README.md", "").
		WithNewDirectory("/subdir2").
		WithNewDirectory("/subdir2/subsubdir").
		WithNewFile("/subdir2/baz.txt", "").
		WithNewFile("/subdir2/TESTING.md", "").
		WithNewFile("/subdir/subsubdir/package.json", "").
		WithNewFile("/subdir/subsubdir/index.mts", "").
		WithNewFile("/subdir/subsubdir/JS.md", "")

	srcSubDir := c.Directory().
		WithDirectory("foobar", srcDir).
		Directory("foobar")

	srcAbsDir := c.Directory().
		WithDirectory("/foo/bar", srcDir).
		Directory("/foo/bar")

	testCases := []struct {
		name string
		src  *dagger.Directory
	}{
		{
			name: "current directory",
			src:  srcDir,
		},
		{
			name: "sub directory",
			src:  srcSubDir,
		},
		{
			name: "absolute directory",
			src:  srcAbsDir,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			t.Run("include only markdown", func(ctx context.Context, t *testctx.T) {
				entries, err := tc.src.Glob(ctx, "*.md")

				require.NoError(t, err)
				require.ElementsMatch(t, entries, []string{"test.md"})
			})

			t.Run("recursive listing", func(ctx context.Context, t *testctx.T) {
				entries, err := tc.src.Glob(ctx, "**/*")

				require.NoError(t, err)
				require.ElementsMatch(t, entries, []string{
					"func.go", "main.go", "test.md", "foo.txt", "README.txt",
					"subdir/", "subdir/foo.txt", "subdir/README.md",
					"subdir2/", "subdir2/subsubdir/", "subdir2/baz.txt", "subdir2/TESTING.md",
					"subdir/subsubdir/", "subdir/subsubdir/package.json",
					"subdir/subsubdir/index.mts", "subdir/subsubdir/JS.md",
				})
			})

			t.Run("recursive that include only markdown", func(ctx context.Context, t *testctx.T) {
				entries, err := tc.src.Glob(ctx, "**/*.md")

				require.NoError(t, err)
				require.ElementsMatch(t, entries, []string{
					"test.md", "subdir/README.md",
					"subdir2/TESTING.md", "subdir/subsubdir/JS.md",
				})
			})

			t.Run("recursive with complex pattern that include only markdown", func(ctx context.Context, t *testctx.T) {
				entries, err := tc.src.Glob(ctx, "subdir/**/*.md")

				require.NoError(t, err)
				require.ElementsMatch(t, entries, []string{
					"subdir/README.md", "subdir/subsubdir/JS.md",
				})
			})
		})
	}

	t.Run("recursive with directories in the pattern", func(ctx context.Context, t *testctx.T) {
		srcDir := c.Directory().
			WithNewFile("foo/bar.md/w.md", "").
			WithNewFile("foo/bar.md/x.go", "").
			WithNewFile("foo/baz.go/y.md", "").
			WithNewFile("foo/baz.go/z.go", "")

		entries, err := srcDir.Glob(ctx, "**/*.md")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{
			"foo/bar.md/",
			"foo/bar.md/w.md",
			"foo/baz.go/y.md",
		})
	})

	t.Run("sub directory in path", func(ctx context.Context, t *testctx.T) {
		srcDir := c.Directory().
			WithNewFile("foo/bar/x.md", "").
			WithNewFile("foo/bar/y.go", "")

		entries, err := srcDir.Directory("foo").Glob(ctx, "**/*.md")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{"bar/x.md"})
	})

	t.Run("empty sub directory", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithNewDirectory("foo").Glob(ctx, "**/*.md")
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("empty directory", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().Glob(ctx, "**/*")
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("directory doesn't exist", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().Directory("foo").Glob(ctx, "**/*")
		requireErrOut(t, err, "no such file or directory")
	})
}

func (DirectorySuite) TestDigest(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("compute directory digest", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("/foo.txt", "Hello, World!")

		digest, err := dir.Directory("/").Digest(ctx)
		require.NoError(t, err)
		require.Equal(t, "sha256:48ac4b4b73af8a75a3c77e9dc6211d89321c6ed4f13c6987c6837ac58645bd89", digest)
	})

	t.Run("directory digest with same contents should be same", func(ctx context.Context, t *testctx.T) {
		a := c.Directory().WithNewDirectory("a").WithNewFile("a/foo.txt", "Hello, World!")
		b := c.Directory().WithNewDirectory("b").WithNewFile("b/foo.txt", "Hello, World!")

		aDigest, err := a.Directory("a").Digest(ctx)
		require.NoError(t, err)
		bDigest, err := b.Directory("b").Digest(ctx)
		require.NoError(t, err)
		require.Equal(t, aDigest, bDigest)
	})

	t.Run("directory digest with different metadata should be different", func(ctx context.Context, t *testctx.T) {
		fileWithOverwrittenMetadata := c.Directory().WithNewFile("foo.txt", "Hello, World!", dagger.DirectoryWithNewFileOpts{
			Permissions: 0777,
		}).File("foo.txt")
		fileWithDefaultMetadata := c.Directory().WithNewFile("foo.txt", "Hello, World!").File("foo.txt")

		digestFileWithOverwrittenMetadata, err := fileWithOverwrittenMetadata.Digest(ctx)
		require.NoError(t, err)

		digestFileWithDefaultMetadata, err := fileWithDefaultMetadata.Digest(ctx)
		require.NoError(t, err)

		require.NotEqual(t, digestFileWithOverwrittenMetadata, digestFileWithDefaultMetadata)
	})

	t.Run("scratch directory", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory()

		digest, err := dir.Digest(ctx)
		require.NoError(t, err)
		require.Equal(t, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", digest)
	})
}

func (DirectorySuite) TestDirectoryName(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("empty directory name", func(ctx context.Context, t *testctx.T) {
		name, err := c.Directory().Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "/", name)
	})

	t.Run("not found directory", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().Directory("foo").Name(ctx)
		requireErrOut(t, err, "no such file or directory")
	})

	t.Run("structured directory", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithDirectory("nested", c.Directory()).WithDirectory("very/nested", c.Directory())

		t.Run("root directory", func(ctx context.Context, t *testctx.T) {
			rootName, err := dir.Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "/", rootName)
		})

		t.Run("nested directory", func(ctx context.Context, t *testctx.T) {
			nestedName, err := dir.Directory("nested").Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "nested/", nestedName)
		})

		t.Run("very nested directory", func(ctx context.Context, t *testctx.T) {
			veryNestedName, err := dir.Directory("very/nested").Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "nested/", veryNestedName)
		})
	})

	t.Run("git directory", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger#ee32df913f57c876e067bd5ecc159561510b6f50").Head().Tree()

		t.Run("root directory", func(ctx context.Context, t *testctx.T) {
			rootName, err := dir.Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "/", rootName)
		})

		t.Run("nested hidden directory", func(ctx context.Context, t *testctx.T) {
			nestedName, err := dir.Directory(".dagger").Name(ctx)
			require.NoError(t, err)
			require.Equal(t, ".dagger/", nestedName)
		})

		t.Run("nested directory", func(ctx context.Context, t *testctx.T) {
			nestedName, err := dir.Directory("sdk").Directory("go").Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "go/", nestedName)
		})
	})
}

func (DirectorySuite) TestPatch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("basic patch application", func(ctx context.Context, t *testctx.T) {
		// Create a directory with a simple file
		dir := c.Directory().
			WithNewFile("hello.txt", "Hello, World!\n")

		// Create a patch that modifies the file
		patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-Hello, World!
+Hello, Dagger!
`

		// Apply the patch
		patchedDir := dir.WithPatch(patch)

		// Verify the patch was applied
		content, err := patchedDir.File("hello.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Dagger!\n", content)
	})

	t.Run("patching a subdirectory", func(ctx context.Context, t *testctx.T) {
		// Create a directory with a simple file
		dir := c.Directory().
			WithNewFile("sub/hello.txt", "Hello, World!\n").
			Directory("sub")

		// Create a patch that modifies the file
		patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-Hello, World!
+Hello, Dagger!
`

		// Apply the patch
		patchedDir := dir.WithPatch(patch)

		// Verify the patch was applied
		content, err := patchedDir.File("hello.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Dagger!\n", content)
	})

	t.Run("patch adding new file", func(ctx context.Context, t *testctx.T) {
		// Start with an empty directory
		dir := c.Directory()

		// Create a patch that adds a new file
		patch := `--- /dev/null
+++ b/newfile.txt
@@ -0,0 +1 @@
+This is a new file!
`

		// Apply the patch
		patchedDir := dir.WithPatch(patch)

		// Verify the new file was created
		content, err := patchedDir.File("newfile.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "This is a new file!\n", content)
	})

	t.Run("patch deleting file", func(ctx context.Context, t *testctx.T) {
		// Create a directory with a file to delete
		dir := c.Directory().
			WithNewFile("delete-me.txt", "This file will be deleted\n").
			WithNewFile("keep-me.txt", "This file will be kept\n")

		// Create a patch that deletes the file
		patch := `--- a/delete-me.txt
+++ /dev/null
@@ -1 +0,0 @@
-This file will be deleted
`

		// Apply the patch
		patchedDir := dir.WithPatch(patch)

		// Verify the file was deleted
		ents, err := patchedDir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"keep-me.txt"}, ents)
	})

	t.Run("multiple file patch", func(ctx context.Context, t *testctx.T) {
		// Create a directory with multiple files
		dir := c.Directory().
			WithNewFile("file1.txt", "Content 1\n").
			WithNewFile("file2.txt", "Content 2\n")

		// Create a patch that modifies both files
		patch := `--- a/file1.txt
+++ b/file1.txt
@@ -1 +1 @@
-Content 1
+Modified Content 1
--- a/file2.txt
+++ b/file2.txt
@@ -1 +1 @@
-Content 2
+Modified Content 2
`

		// Apply the patch
		patchedDir := dir.WithPatch(patch)

		// Verify both files were modified
		content1, err := patchedDir.File("file1.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Modified Content 1\n", content1)

		content2, err := patchedDir.File("file2.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Modified Content 2\n", content2)
	})

	t.Run("empty patch", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("test.txt", "test content")

		// Create an empty patch
		invalidPatch := ""

		// Apply the empty patch and expect no error
		_, err := dir.WithPatch(invalidPatch).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("bad patch application", func(ctx context.Context, t *testctx.T) {
		// Create a directory with a simple file
		dir := c.Directory().
			WithNewFile("hello.txt", "Hello, World!\n")

		// Create a patch that is looking for the wrong content
		invalidPatch := `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-Goodbye, World!
+Hello, Dagger!
`

		// Apply the invalid patch and expect an error
		_, err := dir.WithPatch(invalidPatch).Sync(ctx)
		require.Error(t, err)
	})
}

func (DirectorySuite) TestSearch(ctx context.Context, t *testctx.T) {
	t.Run("literal search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file1.txt", "Hello, World!\nThis is a test file.\nWorld is great.").
			WithNewFile("file2.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("subdir/file3.txt", "Hello from subdirectory!\nWorld tour.")

		results, err := dir.Search(ctx, "World")
		require.NoError(t, err)

		// Collect all matches to check they're all present (order doesn't matter)
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			absoluteOffset, err := result.AbsoluteOffset(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			submatches, err := result.Submatches(ctx)
			require.NoError(t, err)

			// Verify AbsoluteOffset is reasonable (non-negative)
			require.GreaterOrEqual(t, absoluteOffset, 0)

			// Verify we have at least one submatch for each result
			require.NotEmpty(t, submatches)

			// Verify submatch structure
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Equal(t, len("World"), end-start)
				require.Equal(t, "World", submatchText)
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check that we have all expected matches
		require.Contains(t, matches, "file1.txt:1:Hello, World!\n")
		require.Contains(t, matches, "file1.txt:3:World is great.")
		require.Contains(t, matches, "subdir/file3.txt:2:World tour.")
	})

	t.Run("search beginning with hyphen", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file1.txt", "Hello, World!\nThis is a --test-- file.\nWorld is great.").
			WithNewFile("file2.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("subdir/file3.txt", "Hello from subdirectory!\nWorld tour.")

		results, err := dir.Search(ctx, "-test")
		require.NoError(t, err)

		// Collect all matches to check they're all present (order doesn't matter)
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			absoluteOffset, err := result.AbsoluteOffset(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			submatches, err := result.Submatches(ctx)
			require.NoError(t, err)

			// Verify AbsoluteOffset is reasonable (non-negative)
			require.GreaterOrEqual(t, absoluteOffset, 0)

			// Verify we have at least one submatch for each result
			require.NotEmpty(t, submatches)

			// Verify submatch structure
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Equal(t, len("-test"), end-start)
				require.Equal(t, "-test", submatchText)
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check that we have all expected matches
		require.Contains(t, matches, "file1.txt:2:This is a --test-- file.\n")
	})

	t.Run("files-only search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file1.txt", "Hello, World!\nThis is a test file.\nWorld is great.").
			WithNewFile("file2.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("subdir/file3.txt", "Hello from subdirectory!\nWorld tour.")

		results, err := dir.Search(ctx, "World", dagger.DirectorySearchOpts{
			FilesOnly: true,
		})
		require.NoError(t, err)

		// Collect all matches to check they're all present (order doesn't matter)
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			require.Zero(t, lineNumber)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			require.Empty(t, matchedText)
			matches = append(matches, filePath)
		}

		// Check that we have all expected matches
		require.ElementsMatch(t, matches, []string{"file1.txt", "subdir/file3.txt"})
	})

	t.Run("limiting results", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file1.txt", "Hello, World!\nThis is a test file.\nWorld is great.").
			WithNewFile("file2.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("file3.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("file4.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("file5.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("file6.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("file7.txt", "Hello, Dagger!\nThis is another test file.").
			WithNewFile("subdir/file3.txt", "Hello from subdirectory!\nWorld tour.")

		results, err := dir.Search(ctx, "another", dagger.DirectorySearchOpts{
			Limit: 3,
		})
		require.NoError(t, err)

		// Collect all matches to check they're all present (order doesn't matter)
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			absoluteOffset, err := result.AbsoluteOffset(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			submatches, err := result.Submatches(ctx)
			require.NoError(t, err)

			// Verify AbsoluteOffset is reasonable (non-negative)
			require.GreaterOrEqual(t, absoluteOffset, 0)

			// Verify we have at least one submatch for each result
			require.NotEmpty(t, submatches)

			// Verify submatch structure
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Equal(t, len("another"), end-start)
				require.Equal(t, "another", submatchText)
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check that we have all expected matches
		// Check that we get exactly 3 matches from the possible files
		require.Len(t, matches, 3)

		// Define all possible matches since order is not stable
		possibleMatches := []string{
			"file2.txt:2:This is another test file.",
			"file3.txt:2:This is another test file.",
			"file4.txt:2:This is another test file.",
			"file5.txt:2:This is another test file.",
			"file6.txt:2:This is another test file.",
			"file7.txt:2:This is another test file.",
		}

		// Verify that all 3 matches are from the possible set and are distinct
		matchSet := make(map[string]bool)
		for _, match := range matches {
			require.Contains(t, possibleMatches, match, "unexpected match: %s", match)
			require.False(t, matchSet[match], "duplicate match: %s", match)
			matchSet[match] = true
		}
	})

	t.Run("regex search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("main.go", "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}").
			WithNewFile("test.go", "package main\n\nfunc TestSomething() {\n\t// test code\n}").
			WithNewFile("lib/helper.go", "package lib\n\nfunc Helper() string {\n\treturn \"help\"\n}")

		// Search for function definitions
		results, err := dir.Search(ctx, `func \w+\(\)`)
		require.NoError(t, err)
		require.Len(t, results, 3)

		// Collect all matches to check they're all present
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			absoluteOffset, err := result.AbsoluteOffset(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			submatches, err := result.Submatches(ctx)
			require.NoError(t, err)

			// Verify AbsoluteOffset is reasonable (non-negative)
			require.GreaterOrEqual(t, absoluteOffset, 0)

			// Verify we have at least one submatch for each result
			require.NotEmpty(t, submatches)

			// Verify submatch structure for regex - should match "func <name>()"
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Equal(t, len(submatchText), end-start)
				require.Regexp(t, `func \w+\(\)`, submatchText)
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check that we have all expected matches (order doesn't matter)
		// Note: matches may have trailing newlines, so we use partial matches
		require.Contains(t, matches, "main.go:3:func main() {\n")
		require.Contains(t, matches, "test.go:3:func TestSomething() {\n")
		require.Contains(t, matches, "lib/helper.go:3:func Helper() string {\n")
	})

	t.Run("multiline search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("dir/code.go", `package main

import "fmt"

func main() {
	name := "Alice"
	age := 30
	fmt.Printf("Name: %s, Age: %d\n", name, age)
}

func another() {
	name := "Alice"
	age := 50
	fmt.Printf("Name: %s, Age: %d\n", name, age)
}`)

		// Search for variable assignments
		results, err := dir.Search(ctx, ":= \"Alice\"\n\tage", dagger.DirectorySearchOpts{Multiline: true})
		require.NoError(t, err)
		require.NotEmpty(t, results)

		// Check that we have the expected variable assignments
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check for the specific assignments we expect (may have different formatting)
		require.Contains(t, matches, "dir/code.go:6:\tname := \"Alice\"\n\tage := 30\n")
		require.Contains(t, matches, "dir/code.go:12:\tname := \"Alice\"\n\tage := 50\n")
	})

	t.Run("multiline regexp search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("dir/code.go", `package main

import "fmt"

func main() {
	name := "Alice"
	age := 30
	fmt.Printf("Name: %s, Age: %d\n", name, age)
}

func another() {
	name := "Alice"
	age := 50
	fmt.Printf("Name: %s, Age: %d\n", name, age)
}`)

		// Search for variable assignments
		results, err := dir.Search(ctx, `:= ".*"\n\s+age`, dagger.DirectorySearchOpts{
			Multiline: true,
		})
		require.NoError(t, err)
		require.NotEmpty(t, results)

		// Check that we have the expected variable assignments
		var matches []string
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check for the specific assignments we expect (may have different formatting)
		require.Contains(t, matches, "dir/code.go:6:\tname := \"Alice\"\n\tage := 30\n")
		require.Contains(t, matches, "dir/code.go:12:\tname := \"Alice\"\n\tage := 50\n")
	})

	t.Run("empty directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory()

		results, err := dir.Search(ctx, "anything")
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file.txt", "Hello, World!")

		results, err := dir.Search(ctx, "nonexistent")
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("search in subdirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("root.txt", "Root content").
			WithNewFile("sub/file.txt", "Subdirectory content").
			WithNewFile("sub/deep/file.txt", "Deep subdirectory content")

		subdir := dir.Directory("sub")
		results, err := subdir.Search(ctx, "ubdirectory")
		require.NoError(t, err)
		require.Equal(t, len(results), 2)

		// Get all file paths
		var filePaths []string
		for _, result := range results {
			path, err := result.FilePath(ctx)
			require.NoError(t, err)
			filePaths = append(filePaths, path)
		}

		// Check that we have the expected files (order doesn't matter)
		require.Contains(t, filePaths, "file.txt")
		require.Contains(t, filePaths, "deep/file.txt")
	})

	t.Run("case sensitive search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file.txt", "Hello\nhello\nHELLO")

		results, err := dir.Search(ctx, "Hello")
		require.NoError(t, err)
		require.Len(t, results, 1)
		lineNumber0, err := results[0].LineNumber(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, lineNumber0)
	})

	t.Run("case insensitive search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("file.txt", "Hello\nhello\nHELLO")

		results, err := dir.Search(ctx, "hello", dagger.DirectorySearchOpts{
			Insensitive: true,
		})
		require.NoError(t, err)
		require.Len(t, results, 3)

		// Collect all line numbers to verify we got all matches
		var lineNumbers []int
		for _, result := range results {
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			lineNumbers = append(lineNumbers, lineNumber)
		}
		require.ElementsMatch(t, []int{1, 2, 3}, lineNumbers)
	})

	t.Run("multiple matches in one file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("code.go", `package main

import "fmt"

func main() {
	fmt.Println("Hello")
	fmt.Println("World")
}`)

		results, err := dir.Search(ctx, "fmt")
		require.NoError(t, err)
		require.Len(t, results, 3)

		lineNumber0, err := results[0].LineNumber(ctx)
		require.NoError(t, err)
		lineNumber1, err := results[1].LineNumber(ctx)
		require.NoError(t, err)
		lineNumber2, err := results[2].LineNumber(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, lineNumber0)
		require.Equal(t, 6, lineNumber1)
		require.Equal(t, 7, lineNumber2)
	})

	t.Run("binary files are skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "mkdir -p /testdir && echo 'text content' > /testdir/text.txt && dd if=/dev/urandom of=/testdir/binary.bin bs=1024 count=1 && echo 'text content' >> /testdir/binary.bin"}).
			Directory("/testdir")

		results, err := dir.Search(ctx, "content")
		require.NoError(t, err)

		files := []string{}
		for _, result := range results {
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			files = append(files, filePath)
		}
		require.Equal(t, []string{"text.txt"}, files)
	})

	t.Run("skip hidden files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("visible.txt", "content with target").
			WithNewFile(".hidden.txt", "content with target").
			WithNewFile("subdir/.hidden2.txt", "content with target")

		t.Run("default behavior includes hidden files", func(ctx context.Context, t *testctx.T) {
			results, err := dir.Search(ctx, "target")
			require.NoError(t, err)

			var filePaths []string
			for _, result := range results {
				path, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, path)
			}
			// Default should include hidden files
			require.Contains(t, filePaths, "visible.txt")
			require.Contains(t, filePaths, ".hidden.txt")
			require.Contains(t, filePaths, "subdir/.hidden2.txt")
		})

		t.Run("skipHidden excludes hidden files", func(ctx context.Context, t *testctx.T) {
			results, err := dir.Search(ctx, "target", dagger.DirectorySearchOpts{
				SkipHidden: true,
			})
			require.NoError(t, err)

			var filePaths []string
			for _, result := range results {
				path, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, path)
			}
			// Should only include visible files
			require.Contains(t, filePaths, "visible.txt")
			require.NotContains(t, filePaths, ".hidden.txt")
			require.NotContains(t, filePaths, "subdir/.hidden2.txt")
		})
	})

	t.Run("skip ignored files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("tracked.txt", "content with target").
			WithNewFile("ignored.log", "content with target").
			WithNewFile("build/output.bin", "content with target").
			WithNewFile(".rgignore", "*.log\nbuild/")

		t.Run("default behavior includes ignored files", func(ctx context.Context, t *testctx.T) {
			results, err := dir.Search(ctx, "target")
			require.NoError(t, err)

			var filePaths []string
			for _, result := range results {
				path, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, path)
			}
			// Default should include ignored files
			require.Contains(t, filePaths, "tracked.txt")
			require.Contains(t, filePaths, "ignored.log")
			require.Contains(t, filePaths, "build/output.bin")
		})

		t.Run("skipIgnored respects rgignore", func(ctx context.Context, t *testctx.T) {
			results, err := dir.Search(ctx, "target", dagger.DirectorySearchOpts{
				SkipIgnored: true,
			})
			require.NoError(t, err)

			var filePaths []string
			for _, result := range results {
				path, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, path)
			}
			// Should only include tracked files
			require.Contains(t, filePaths, "tracked.txt")
			require.NotContains(t, filePaths, "ignored.log")
			require.NotContains(t, filePaths, "build/output.bin")
		})
	})

	t.Run("globs option", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("main.go", "package main\nfunc main() { fmt.Println(\"hello\") }").
			WithNewFile("test.go", "package main\nfunc TestSomething() { /* test code */ }").
			WithNewFile("README.md", "# Project\nThis is a documentation file").
			WithNewFile("config.yaml", "version: 1\nname: test-project").
			WithNewFile("lib/helper.go", "package lib\nfunc Helper() string { return \"help\" }")

		t.Run("single glob pattern", func(ctx context.Context, t *testctx.T) {
			// Search for "func" only in .go files
			results, err := dir.Search(ctx, "func", dagger.DirectorySearchOpts{
				Globs: []string{"*.go"},
			})
			require.NoError(t, err)
			require.Len(t, results, 3) // main(), TestSomething(), Helper()

			// Verify all results are from .go files
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				require.True(t, strings.HasSuffix(filePath, ".go"), "Expected .go file, got: %s", filePath)
			}
		})

		t.Run("multiple glob patterns", func(ctx context.Context, t *testctx.T) {
			// Search for "test" in both .go and .md files
			results, err := dir.Search(ctx, "test", dagger.DirectorySearchOpts{
				Globs: []string{"*.go", "*.md"},
			})
			require.NoError(t, err)
			// Only test.go contains "test" (TestSomething function) - README.md doesn't contain "test"
			require.Len(t, results, 1)

			result := results[0]
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			require.Equal(t, "test.go", filePath)
		})

		t.Run("glob with subdirectories", func(ctx context.Context, t *testctx.T) {
			// Search for "func" in all .go files, including subdirectories
			results, err := dir.Search(ctx, "func", dagger.DirectorySearchOpts{
				Globs: []string{"**/*.go"},
			})
			require.NoError(t, err)
			require.Len(t, results, 3) // main(), TestSomething(), Helper()

			var filePaths []string
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, filePath)
			}
			require.Contains(t, filePaths, "main.go")
			require.Contains(t, filePaths, "test.go")
			require.Contains(t, filePaths, "lib/helper.go")
		})

		t.Run("glob with no matches", func(ctx context.Context, t *testctx.T) {
			// Search for a pattern that exists in files but not in the files matching the glob
			results, err := dir.Search(ctx, "main", dagger.DirectorySearchOpts{
				Globs: []string{"*.md", "*.yaml"}, // Only search in markdown and yaml files where "main" doesn't appear
			})
			require.NoError(t, err)
			require.Empty(t, results)
		})
	})

	t.Run("paths option", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("src/main.go", "package main\nfunc main() { fmt.Println(\"hello world\") }").
			WithNewFile("src/helper.go", "package main\nfunc Helper() { return \"world peace\" }").
			WithNewFile("tests/main_test.go", "package main\nfunc TestMain() { /* world testing */ }").
			WithNewFile("docs/README.md", "# Project\nHello world documentation").
			WithNewFile("config/app.yaml", "name: world-app\nversion: 1.0").
			WithNewFile("etc/passwd", "root:world passwd").
			WithSymlink("/etc", "symlink")

		t.Run("single path", func(ctx context.Context, t *testctx.T) {
			// Search for "world" only in src directory
			results, err := dir.Search(ctx, "world", dagger.DirectorySearchOpts{
				Paths: []string{"src"},
			})
			require.NoError(t, err)
			require.Len(t, results, 2) // "hello world" and "world peace"

			// Verify all results are from src directory
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(filePath, "src/"), "Expected src/ path, got: %s", filePath)
			}
		})

		t.Run("multiple paths", func(ctx context.Context, t *testctx.T) {
			// Search for "world" in both src and docs directories
			results, err := dir.Search(ctx, "world", dagger.DirectorySearchOpts{
				Paths: []string{"src", "docs"},
			})
			require.NoError(t, err)
			require.Len(t, results, 3) // src/main.go, src/helper.go, docs/README.md

			var filePaths []string
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, filePath)
				// Should be in src or docs directory
				require.True(t,
					strings.HasPrefix(filePath, "src/") || strings.HasPrefix(filePath, "docs/"),
					"Expected src/ or docs/ path, got: %s", filePath)
			}
			// config/ and tests/ should be excluded
			for _, path := range filePaths {
				require.False(t, strings.HasPrefix(path, "config/"))
				require.False(t, strings.HasPrefix(path, "tests/"))
			}
		})

		t.Run("specific file path", func(ctx context.Context, t *testctx.T) {
			// Search for "main" in a specific file
			results, err := dir.Search(ctx, "main", dagger.DirectorySearchOpts{
				Paths: []string{"src/main.go"},
			})
			require.NoError(t, err)
			require.Len(t, results, 2) // "package main" and "func main"

			// Verify all results are from the specific file
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				require.Equal(t, "src/main.go", filePath)
			}
		})

		t.Run("path with no matches", func(ctx context.Context, t *testctx.T) {
			// Search for non-existent pattern in existing directory
			results, err := dir.Search(ctx, "nonexistent-pattern", dagger.DirectorySearchOpts{
				Paths: []string{"src"},
			})
			require.NoError(t, err)
			require.Empty(t, results)
		})

		t.Run("normalizes absolute paths", func(ctx context.Context, t *testctx.T) {
			// Test that absolute paths are treated as relative to the directory
			results, err := dir.Search(ctx, "world", dagger.DirectorySearchOpts{
				Paths: []string{"/src"},
			})
			require.NoError(t, err)
			require.Len(t, results, 2) // Should find results in src directory

			// Verify all results are from src directory
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(filePath, "src/"), "Expected src/ path, got: %s", filePath)
			}
		})

		t.Run("keeps symlinks within the directory", func(ctx context.Context, t *testctx.T) {
			// Test that symlinks are resolved within the directory
			results, err := dir.Search(ctx, "root", dagger.DirectorySearchOpts{
				Paths: []string{"symlink"},
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			matched, err := results[0].MatchedLines(ctx)
			require.NoError(t, err)
			require.Equal(t, "root:world passwd", matched)

			// Test that we don't allow naively evaluating symlinks by following them
			// first (e.g. symlink/passwd => /etc/passwd)
			results, err = dir.Search(ctx, "root", dagger.DirectorySearchOpts{
				Paths: []string{"symlink/passwd"},
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			matched, err = results[0].MatchedLines(ctx)
			require.NoError(t, err)
			require.Equal(t, "root:world passwd", matched)
		})

		t.Run("resolves symlinks within a scoped dir", func(ctx context.Context, t *testctx.T) {
			dir := c.Directory().
				WithNewFile("symlink", "im innocent").
				WithNewFile("subdir/etc/passwd", "root:world passwd").
				WithSymlink("/etc/passwd", "subdir/symlink")

			results, err := dir.Directory("subdir").Search(ctx, "root", dagger.DirectorySearchOpts{
				Paths: []string{"symlink"},
			})
			require.NoError(t, err)
			require.Len(t, results, 1)
			matched, err := results[0].MatchedLines(ctx)
			require.NoError(t, err)
			// make sure we correctly resolve the path WITHIN the directory - in the
			// failure case this will have content from `/etc/passwd` on the host!
			require.Equal(t, "root:world passwd", matched)
		})

		t.Run("rejects paths that escape directory", func(ctx context.Context, t *testctx.T) {
			// Test that paths trying to escape via .. are rejected
			_, err := dir.Search(ctx, "world", dagger.DirectorySearchOpts{
				Paths: []string{"../../etc/passwd"},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "path cannot escape directory")
		})

		t.Run("rejects nested directory escape attempts", func(ctx context.Context, t *testctx.T) {
			// Test that paths containing ".." anywhere that would escape are rejected
			_, err := dir.Search(ctx, "world", dagger.DirectorySearchOpts{
				Paths: []string{"some/../../etc/passwd"},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "path cannot escape directory")
		})

		t.Run("allows valid relative paths with double dots", func(ctx context.Context, t *testctx.T) {
			// Test that valid relative paths that don't escape still work
			results, err := dir.Search(ctx, "package main", dagger.DirectorySearchOpts{
				Paths: []string{"/src/../"},
			})
			require.NoError(t, err)
			require.Len(t, results, 3)
		})
	})

	t.Run("combined globs and paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		dir := c.Directory().
			WithNewFile("src/main.go", "package main\nfunc main() { fmt.Println(\"hello\") }").
			WithNewFile("src/helper.js", "function helper() { console.log('hello'); }").
			WithNewFile("tests/main_test.go", "package main\nfunc TestMain() { /* hello testing */ }").
			WithNewFile("tests/helper_test.js", "function testHelper() { console.log('hello'); }")

		t.Run("globs and paths together", func(ctx context.Context, t *testctx.T) {
			// Search for "hello" in .go files within src directory only
			results, err := dir.Search(ctx, "hello", dagger.DirectorySearchOpts{
				Globs: []string{"*.go"},
				Paths: []string{"src"},
			})
			require.NoError(t, err)
			require.Len(t, results, 1) // Only src/main.go should match

			result := results[0]
			filePath, err := result.FilePath(ctx)
			require.NoError(t, err)
			require.Equal(t, "src/main.go", filePath)
		})

		t.Run("globs and paths with multiple patterns", func(ctx context.Context, t *testctx.T) {
			// Search for "hello" in both .go and .js files within tests directory
			results, err := dir.Search(ctx, "hello", dagger.DirectorySearchOpts{
				Globs: []string{"*.go", "*.js"},
				Paths: []string{"tests"},
			})
			require.NoError(t, err)
			require.Len(t, results, 2) // tests/main_test.go and tests/helper_test.js

			var filePaths []string
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)
				filePaths = append(filePaths, filePath)
				require.True(t, strings.HasPrefix(filePath, "tests/"))
			}
			require.Contains(t, filePaths, "tests/main_test.go")
			require.Contains(t, filePaths, "tests/helper_test.js")
		})
	})
}

func (DirectorySuite) TestSymlink(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("symlink in same directory", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("some-file", "some-content").
			WithSymlink("some-file", "symlink-to-some-file")

		ctr := c.Container().From(alpineImage).WithDirectory("/test-dir", dir)

		// test the symlink is an actual symlink
		_, err := ctr.WithExec([]string{"test", "-L", "/test-dir/symlink-to-some-file"}).Stdout(ctx)
		require.NoError(t, err)

		symlinkContents, err := dir.File("symlink-to-some-file").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "some-content", symlinkContents)

		symlinkContents, err = dir.WithNewFile("some-file", "overwritten-contents").File("symlink-to-some-file").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "overwritten-contents", symlinkContents)
	})

	t.Run("symlink to parent directory", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithSymlink("../root", "its-root")
		f := c.Container().From(alpineImage).
			WithDirectory("/test-dir", dir).
			WithNewFile("/test-dir/its-root/f", "data").
			File("/root/f")
		s, err := f.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	t.Run("symlink with abs path", func(ctx context.Context, t *testctx.T) {
		s, err := c.Directory().
			WithNewFile("/some-file", "some-content").
			WithSymlink("/some-file", "/symlink-to-some-file").
			File("symlink-to-some-file").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "some-content", s)
	})

	t.Run("symlink with abs path mounted as a subdir", func(ctx context.Context, t *testctx.T) {
		d := c.Directory().
			WithNewFile("/some-file", "some-content").
			WithSymlink("/some-file", "/symlink-to-some-file")

		d2 := c.Directory().
			WithNewFile("/some-file", "other-content").
			WithDirectory("/sub-dir", d)

		s, err := d2.
			File("sub-dir/symlink-to-some-file").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "other-content", s)
	})

	t.Run("symlink creates parent dirs", func(ctx context.Context, t *testctx.T) {
		s, err := c.Directory().
			WithNewFile("/some-file", "some-content").
			WithSymlink("../../some-file", "/sub/subdir/symlink").
			File("/sub/subdir/symlink").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "some-content", s)
	})

	t.Run("symlink correctly passes dir path", func(ctx context.Context, t *testctx.T) {
		d := c.Directory().WithNewFile("some-file", "data")

		d2 := c.Directory().
			WithNewFile("some-other-file", "other-data").
			WithNewDirectory("dir1").
			Directory("/dir1").
			WithDirectory("/", d).
			WithSymlink("anything", "link")

		// this should no longer be available, since dir.Dir should now be "/dir1"
		_, err := d2.File("some-other-file").Contents(ctx)
		require.Error(t, err)

		s, err := d2.File("some-file").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	t.Run("symlink follows symlinks in dir path but not basename", func(ctx context.Context, t *testctx.T) {
		s, err := c.Directory().
			WithNewDirectory("dir1").
			WithSymlink("dir1", "dir2").
			WithSymlink("file", "dir2/symlink").
			WithoutFile("dir2").
			WithNewFile("dir1/file", "data").
			File("/dir1/symlink").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	t.Run("symlink errors rather when symlink already exists", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().
			WithSymlink("target", "symlink").
			WithSymlink("newtarget", "symlink").
			Sync(ctx)

		require.Error(t, err)
		require.Regexp(t, "symlink newtarget /var/lib/dagger/worker/cachemounts/.*/symlink: file exists", err.Error())
	})
}

func (DirectorySuite) TestExists(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		Description         string
		Path                string
		Type                dagger.ExistsType
		Expected            bool
		DoNotFollowSymlinks bool
		ErrorContains       string
	}{
		{
			Description: "test exists is false when referencing a non-existent file",
			Path:        "We believe in nothing Lebowski",
			Type:        "",
			Expected:    false,
		},
		{
			Description: "test existence works on a directory without specifying an expected type",
			Path:        "quotes",
			Type:        "",
			Expected:    true,
		},
		{
			Description: "test is a directory",
			Path:        "quotes",
			Type:        dagger.ExistsTypeDirectoryType,
			Expected:    true,
		},
		{
			Description: "test is a directory fails when referencing a file that exists",
			Path:        "quotes/descartes",
			Type:        dagger.ExistsTypeDirectoryType,
			Expected:    false,
		},
		{
			Description: "test is a file works",
			Path:        "quotes/descartes",
			Type:        dagger.ExistsTypeRegularType,
			Expected:    true,
		},
		{
			Description: "test is a file fails when referencing a directory that exists",
			Path:        "quotes",
			Type:        dagger.ExistsTypeRegularType,
			Expected:    false,
		},
		{
			Description: "test is a symlink works",
			Path:        "i-am",
			Type:        dagger.ExistsTypeSymlinkType,
			Expected:    true,
		},
		{
			Description: "test symlink fails",
			Path:        "quotes/descartes",
			Type:        dagger.ExistsTypeSymlinkType,
			Expected:    false,
		},

		// this is to follow the same functionality as the `test` shell command,
		// from `man test`, it states that "all FILE-related tests dereference symbolic links (except -h and -L, which don't apply here)",
		// so we should do the same.
		{
			Description: "test is a file works on a symlink",
			Path:        "i-am",
			Type:        dagger.ExistsTypeRegularType,
			Expected:    true,
		},
		{
			Description:         "test DoNotFollowSymlinks is true when target does not exist",
			Path:                "nothing",
			DoNotFollowSymlinks: true,
			Expected:            true,
		},
		{
			Description:         "test DoNotFollowSymlinks prevents regular file type from being true when referencing a symlink",
			Path:                "i-am",
			Type:                dagger.ExistsTypeRegularType,
			DoNotFollowSymlinks: true,
			Expected:            false,
		},
	} {
		t.Run(tc.Description, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			quotesDir := c.Directory().WithNewFile("quotes/descartes", "Cogito, ergo sum").WithSymlink("quotes/descartes", "i-am").WithSymlink("quotes/does-not-exist", "nothing")
			exists, err := quotesDir.Exists(ctx, tc.Path, dagger.DirectoryExistsOpts{
				ExpectedType:        tc.Type,
				DoNotFollowSymlinks: tc.DoNotFollowSymlinks,
			})
			if tc.ErrorContains == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.ErrorContains)
			}
			require.Equal(t, tc.Expected, exists)
		})
	}
}

func (DirectorySuite) TestDirCaching(ctx context.Context, t *testctx.T) {
	// NOTE: This test requires that WithNewFile sets the creation date to the current time,
	// if this side-effect were to ever change (i.e. adopting SOURCE_DATE_EPOCH functionality),
	// then this test will break.

	c := connect(ctx, t)
	d1 := c.Directory().
		WithoutFile("non-existent").
		WithNewFile("file", "data")

	d2 := c.Directory().
		WithoutFile("also-non-existent").
		WithNewFile("file", "data")

	out, err := c.Container().
		From(alpineImage).
		WithMountedDirectory("/d1", d1).
		WithMountedDirectory("/d2", d2).
		WithExec([]string{"sh", "-c", "diff <(stat /d1/file | grep Modify) <(stat /d2/file | grep Modify)"}). // they should be the exact same file (i.e. same creation time)
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, out, "")

	d3 := c.Directory().
		WithNewFile("not", "used").
		WithNewFile("file", "data")

	out, err = c.Container().
		From(alpineImage).
		WithMountedDirectory("/d1", d1).
		WithMountedDirectory("/d3", d3).
		WithExec([]string{"sh", "-c", "! diff <(stat /d1/file | grep Modify) <(stat /d3/file | grep Modify)"}). // should be different since "not", "used" file still changed the disk
		Stdout(ctx)
	require.NoError(t, err)
	require.NotEqual(t, out, "")
}
