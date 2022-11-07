package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestEmptyDirectory(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			ID      core.DirectoryID
			Entries []string
		}
	}

	err := testutil.Query(
		`{
			directory {
				id
				entries
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Directory.ID)
	require.Empty(t, res.Directory.Entries)
}

func TestDirectoryWithNewFile(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				ID      core.DirectoryID
				Entries []string
			}
		}
	}

	err := testutil.Query(
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

func TestDirectoryEntries(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(
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
	require.ElementsMatch(t, []string{"some-file", "some-dir"}, res.Directory.WithNewFile.WithNewFile.Entries)
}

func TestDirectoryEntriesOfPath(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(
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

func TestDirectoryDirectory(t *testing.T) {
	t.Parallel()

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

	err := testutil.Query(
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

func TestDirectoryDirectoryWithNewFile(t *testing.T) {
	t.Parallel()

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

	err := testutil.Query(
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

func TestDirectoryWithDirectory(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	dir := c.Directory().
		WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
			Contents: "some-content",
		}).
		WithNewFile("some-dir/sub-file", dagger.DirectoryWithNewFileOpts{
			Contents: "sub-content",
		}).
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

	t.Run("copies directory contents to .", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, entries)
	})
}

func TestDirectoryWithDirectoryIncludeExclude(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	dir := c.Directory().
		WithNewFile("a.txt").
		WithNewFile("b.txt").
		WithNewFile("c.txt.rar").
		WithNewFile("subdir/d.txt").
		WithNewFile("subdir/e.txt").
		WithNewFile("subdir/f.txt.rar")

	t.Run("exclude", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "subdir"}, entries)
	})

	t.Run("include", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar"}, entries)
	})

	t.Run("exclude overrides include", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt"}, entries)
	})

	t.Run("include does not override exclude", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{}, entries)
	})

	subdir := dir.Directory("subdir")

	t.Run("exclude respects subdir", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", subdir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"d.txt", "e.txt"}, entries)
	})
}

func TestDirectoryWithNewDirectory(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	dir := c.Directory().
		WithNewDirectory("a").
		WithNewDirectory("b/c")

	entries, err := dir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, entries)

	entries, err = dir.Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "b",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"c"}, entries)

	t.Run("does not permit creating directory outside of root", func(t *testing.T) {
		_, err := dir.Directory("b").WithNewDirectory("../c").ID(ctx)
		require.Error(t, err)
	})
}

func TestDirectoryWithFile(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	file := c.Directory().
		WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
			Contents: "some-content",
		}).
		File("some-file")

	content, err := c.Directory().
		WithFile("target-file", file).
		File("target-file").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", content)

	content, err = c.Directory().
		WithFile("sub-dir/target-file", file).
		File("sub-dir/target-file").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", content)
}

func TestDirectoryWithoutDirectory(t *testing.T) {
	t.Parallel()

	dirID := newDirWithFiles(t,
		"some-file", "some-content",
		"some-dir/sub-file", "sub-content")

	var res2 struct {
		Directory struct {
			WithDirectory struct {
				WithoutDirectory struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(
		`query Test($src: DirectoryID!) {
			directory {
				withDirectory(path: "with-dir", directory: $src) {
					withoutDirectory(path: "with-dir/some-dir") {
						entries(path: "with-dir")
					}
				}
			}
		}`, &res2, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": dirID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, []string{"some-file"}, res2.Directory.WithDirectory.WithoutDirectory.Entries)
}

func TestDirectoryWithoutFile(t *testing.T) {
	t.Parallel()

	dirID := newDirWithFiles(t,
		"some-file", "some-content",
		"some-dir/sub-file", "sub-content")

	var res2 struct {
		Directory struct {
			WithDirectory struct {
				WithoutFile struct {
					Entries []string
				}
			}
		}
	}

	err := testutil.Query(
		`query Test($src: DirectoryID!) {
			directory {
				withDirectory(path: "with-dir", directory: $src) {
					withoutFile(path: "with-dir/some-file") {
						entries(path: "with-dir")
					}
				}
			}
		}`, &res2, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": dirID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, []string{"some-dir"}, res2.Directory.WithDirectory.WithoutFile.Entries)
}

func TestDirectoryDiff(t *testing.T) {
	t.Parallel()

	aID := newDirWithFile(t, "a-file", "a-content")
	bID := newDirWithFile(t, "b-file", "b-content")

	var res struct {
		Directory struct {
			Diff struct {
				Entries []string
			}
		}
	}

	diff := `query Diff($id: DirectoryID!, $other: DirectoryID!) {
			directory(id: $id) {
				diff(other: $other) {
					entries
				}
			}
		}`
	err := testutil.Query(diff, &res, &testutil.QueryOptions{
		Variables: map[string]any{
			"id":    aID,
			"other": bID,
		},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"b-file"}, res.Directory.Diff.Entries)

	err = testutil.Query(diff, &res, &testutil.QueryOptions{
		Variables: map[string]any{
			"id":    bID,
			"other": aID,
		},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"a-file"}, res.Directory.Diff.Entries)

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

func TestDirectoryExport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	wd := t.TempDir()
	dest := t.TempDir()

	c, err := dagger.Connect(ctx, dagger.WithWorkdir(wd))
	require.NoError(t, err)
	defer c.Close()

	dir := c.Container().From("alpine:3.16.2").Directory("/etc/profile.d")

	t.Run("to absolute dir", func(t *testing.T) {
		ok, err := dir.Export(ctx, dest)
		require.NoError(t, err)
		require.True(t, ok)

		entries, err := ls(dest)
		require.NoError(t, err)
		require.Equal(t, []string{"README", "color_prompt.sh.disabled", "locale.sh"}, entries)
	})

	t.Run("to workdir", func(t *testing.T) {
		ok, err := dir.Export(ctx, ".")
		require.NoError(t, err)
		require.True(t, ok)

		entries, err := ls(wd)
		require.NoError(t, err)
		require.Equal(t, []string{"README", "color_prompt.sh.disabled", "locale.sh"}, entries)
	})

	t.Run("to outer dir", func(t *testing.T) {
		ok, err := dir.Export(ctx, "../")
		require.Error(t, err)
		require.False(t, ok)
	})
}
