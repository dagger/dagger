package core

import (
	"testing"

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

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						ID core.DirectoryID
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
							id
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)

	var res2 struct {
		Directory struct {
			WithDirectory struct {
				Entries []string
			}
		}
	}

	err = testutil.Query(
		`query Test($src: DirectoryID!) {
			directory {
				withDirectory(path: "with-dir", directory: $src) {
					entries(path: "with-dir")
				}
			}
		}`, &res2, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": res.Directory.WithNewFile.WithNewFile.Directory.ID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res2.Directory.WithDirectory.Entries)

	err = testutil.Query(
		`query Test($src: DirectoryID!) {
			directory {
				withDirectory(path: "sub-dir/sub-sub-dir/with-dir", directory: $src) {
					entries(path: "sub-dir/sub-sub-dir/with-dir")
				}
			}
		}`, &res2, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": res.Directory.WithNewFile.WithNewFile.Directory.ID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res2.Directory.WithDirectory.Entries)
}

func TestDirectoryWithCopiedFile(t *testing.T) {
	var fileRes struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.DirectoryID
				}
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					file(path: "some-file") {
						id
					}
				}
			}
		}`, &fileRes, nil)
	require.NoError(t, err)
	require.NotEmpty(t, fileRes.Directory.WithNewFile.File.ID)

	var res struct {
		Directory struct {
			WithCopiedFile struct {
				File struct {
					ID       core.DirectoryID
					Contents string
				}
			}
		}
	}

	err = testutil.Query(
		`query Test($src: FileID!) {
			directory {
				withCopiedFile(path: "target-file", source: $src) {
					file(path: "target-file") {
						id
						contents
					}
				}
			}
		}`, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": fileRes.Directory.WithNewFile.File.ID,
			},
		})
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithCopiedFile.File.ID)
	require.Equal(t, "some-content", res.Directory.WithCopiedFile.File.Contents)
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
