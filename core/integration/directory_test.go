package core

import (
	"context"
	"fmt"
	"os"
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
	var res struct {
		Directory struct {
			ID      core.DirectoryID
			Entries []string
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				entries
			}
		}`, &res, nil, dagger.WithLogOutput(os.Stderr))
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
	var res struct {
		Directory struct {
			WithNewFile struct {
				ID      core.DirectoryID
				Entries []string
			}
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
					entries
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.ID)
	require.Equal(t, []string{"some-file"}, res.Directory.WithNewFile.Entries)
}

func (DirectorySuite) TestEntries(ctx context.Context, t *testctx.T) {
	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						entries
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"some-file", "some-dir/"}, res.Directory.WithNewFile.WithNewFile.Entries)
}

func (DirectorySuite) TestEntriesOfPath(ctx context.Context, t *testctx.T) {
	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						entries(path: "some-dir")
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Entries)
}

func (DirectorySuite) TestDirectory(ctx context.Context, t *testctx.T) {
	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						Entries []string
					}
				}
			}
		}
	}

	err := testutil.Query(t,
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
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Directory.Entries)
}

func (DirectorySuite) TestDirectoryWithNewFile(ctx context.Context, t *testctx.T) {
	var res struct {
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
	}

	err := testutil.Query(t,
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
		}`, &res, nil)
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
		_, err := dir.Directory("b").WithNewDirectory("../c").ID(ctx)
		require.Error(t, err)
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

		var res struct {
			Directory struct {
				Diff struct {
					Entries []string
				}
			} `json:"loadDirectoryFromID"`
		}

		diff := `query Diff($id: DirectoryID!, $other: DirectoryID!) {
			loadDirectoryFromID(id: $id) {
				diff(other: $other) {
					entries
				}
			}
		}`
		err := testutil.Query(t, diff, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"id":    aID,
				"other": bID,
			},
		})
		require.NoError(t, err)

		require.Equal(t, []string{"b-file"}, res.Directory.Diff.Entries)

		err = testutil.Query(t, diff, &res, &testutil.QueryOptions{
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
		require.Error(t, err)
		require.Empty(t, actual)
	})
}

func (DirectorySuite) TestDockerBuild(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := c.Directory().
		WithNewFile("main.go",
			`package main
import "fmt"
import "os"
func main() {
	for _, env := range os.Environ() {
		fmt.Println(env)
	}
}`)

	t.Run("default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		env, err := src.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		env, err := src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := sub.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := sub.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with build args", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
ARG FOOARG=bar
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=$FOOARG
CMD goenv
`)

		env, err := src.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		env, err = src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}},
		}).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=barbar\n")
	})

	t.Run("with target", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine AS base
CMD echo "base"

FROM base AS stage1
CMD echo "stage1"

FROM base AS stage2
CMD echo "stage2"
`)

		output, err := src.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		output, err = src.DockerBuild(dagger.DirectoryDockerBuildOpts{Target: "stage1"}).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})

	t.Run("with build secrets", func(ctx context.Context, t *testctx.T) {
		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/foo").
			With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/foo/internal/dagger"
)

type Foo struct{}

func (m *Foo) DirBuild(ctx context.Context, dir *dagger.Directory, mySecret *dagger.Secret) (string, error) {
	return dir.DockerBuild(dagger.DirectoryDockerBuildOpts{
		SecretArgs: []dagger.SecretArg{
			{
				Name:  "my-secret",
				Value: mySecret,
			},
		},
	}).
		WithExec(nil).Stdout(ctx)
}

`)
		dockerfile := `FROM golang:1.18.2-alpine
WORKDIR /src
RUN --mount=type=secret,id=my-secret,required=true test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret,required=true cp /run/secrets/my-secret /secret
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.
				WithNewFile("Dockerfile", dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("ctr-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("ctr-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})
	})

	t.Run("with no .dockerignore", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo.txt", "content in foo file").
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
		WORKDIR /src
		COPY . .
		`)

		output, err := src.DockerBuild().Directory("/src").File("foo.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "content in foo file")
	})

	t.Run("use .dockerignore file by default", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo.txt", "content in foo file").
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
	WORKDIR /src
	COPY . .
	`).
			WithNewFile(".dockerignore", "foo.txt")

		_, err := src.DockerBuild().Directory("/src").File("foo.txt").Contents(ctx)
		require.ErrorContains(t, err, "/src/foo.txt: no such file or directory")
	})

	t.Run("ignores .dockerignore for dockerfile with name 'myfoo' if myfoo.dockerignore exists", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo.txt", "content in foo file").
			WithNewFile("bar.txt", "content in bar file").
			WithNewFile("myfoo",
				`FROM golang:1.18.2-alpine
		WORKDIR /src
		COPY . .
		`).
			WithNewFile(".dockerignore", "foo.txt").
			WithNewFile("myfoo.dockerignore", "bar.txt")

		_, err := src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "myfoo",
		}).Directory("/src").File("bar.txt").Contents(ctx)
		require.ErrorContains(t, err, "/src/bar.txt: no such file or directory")

		content, err := src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "myfoo",
		}).Directory("/src").File("foo.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content in foo file", content)
	})

	t.Run("ignores .dockerignore for dockerfile with name 'Dockerfile' if Dockerfile.dockerignore exists", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo.txt", "content in foo file").
			WithNewFile("bar.txt", "content in bar file").
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
		WORKDIR /src
		COPY . .
		`).
			WithNewFile(".dockerignore", "foo.txt").
			WithNewFile("Dockerfile.dockerignore", "bar.txt")

		_, err := src.DockerBuild().Directory("/src").File("bar.txt").Contents(ctx)
		require.ErrorContains(t, err, "/src/bar.txt: no such file or directory")

		content, err := src.DockerBuild().Directory("/src").File("foo.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content in foo file", content)
	})

	t.Run("use .dockerignore for dockerfile with name 'myfoo' if myfoo.dockerignore does not exist", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo.txt", "content in foo file").
			WithNewFile("myfoo",
				`FROM golang:1.18.2-alpine
		WORKDIR /src
		COPY . .
		`).
			WithNewFile(".dockerignore", "foo.txt")

		_, err := src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "myfoo",
		}).Directory("/src").File("foo.txt").Contents(ctx)
		require.ErrorContains(t, err, "/src/foo.txt: no such file or directory")
	})

	// https://github.com/moby/buildkit/blob/36b0458ff396aef565849e8ec112e7b12088d83d/frontend/dockerfile/dockerfile_test.go#L3534
	t.Run("confirm .dockerignore compatibility with docker", func(ctx context.Context, t *testctx.T) {
		src := contextDir.
			WithNewFile("foo", "foo-contents").
			WithNewFile("bar", "bar-contents").
			WithNewFile("baz", "baz-contents").
			WithNewFile("bay", "bay-contents").
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
	WORKDIR /src
	COPY . .
	`).
			WithNewFile(".dockerignore", `
	ba*
	Dockerfile
	!bay
	.dockerignore
	`)

		content, err := src.DockerBuild().Directory("/src").File("foo").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo-contents", content)

		cts, err := src.DockerBuild().Directory("/src").File(".dockerignore").Contents(ctx)
		require.ErrorContains(t, err, "/src/.dockerignore: no such file or directory", fmt.Sprintf("cts is %s", cts))

		_, err = src.DockerBuild().Directory("/src").File("Dockerfile").Contents(ctx)
		require.ErrorContains(t, err, "/src/Dockerfile: no such file or directory")

		_, err = src.DockerBuild().Directory("/src").File("bar").Contents(ctx)
		require.ErrorContains(t, err, "/src/bar: no such file or directory")

		_, err = src.DockerBuild().Directory("/src").File("baz").Contents(ctx)
		require.ErrorContains(t, err, "/src/baz: no such file or directory")

		content, err = src.DockerBuild().Directory("/src").File("bay").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bay-contents", content)
	})
}

func (DirectorySuite) TestWithNewFileExceedingLength(ctx context.Context, t *testctx.T) {
	var res struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				withNewFile(path: "bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt", contents: "some-content") {
					id
				}
			}
		}`, &res, nil)
	require.Error(t, err)
	requireErrOut(t, err, "File name length exceeds the maximum supported 255 characters")
}

func (DirectorySuite) TestWithFileExceedingLength(ctx context.Context, t *testctx.T) {
	var res struct {
		Directory struct {
			WithNewFile struct {
				file struct {
					ID core.DirectoryID
				}
			}
		}
	}

	err := testutil.Query(t,
		`{
			directory {
				withNewFile(path: "dir/bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt", contents: "some-content") {
					file(path: "dir/bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt") {
						id
					}
				}
			}
		}`, &res, nil)
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
		require.Equal(t, inode, strings.TrimSpace(out))
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
