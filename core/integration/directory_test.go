package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/internal/testutil"
)

func TestEmptyDirectory(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			ID       core.DirectoryID
			Contents []string
		}
	}

	err := testutil.Query(
		`{
			directory {
				id
				contents
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Directory.ID)
	require.Empty(t, res.Directory.Contents)
}

func TestDirectoryWithNewFile(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				ID       core.DirectoryID
				Contents []string
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
					contents
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.ID)
	require.Equal(t, []string{"some-file"}, res.Directory.WithNewFile.Contents)
}

func TestDirectoryContents(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Contents []string
				}
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						contents
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"some-file", "some-dir"}, res.Directory.WithNewFile.WithNewFile.Contents)
}

func TestDirectoryContentsOfPath(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Contents []string
				}
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "some-content") {
						contents(path: "some-dir")
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Contents)
}

func TestDirectoryDirectory(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						Contents []string
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
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res.Directory.WithNewFile.WithNewFile.Directory.Contents)
}

func TestDirectoryDirectoryWithNewFile(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						WithNewFile struct {
							Contents []string
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
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]string{"sub-file", "another-file"},
		res.Directory.WithNewFile.WithNewFile.Directory.WithNewFile.Contents)
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
				Contents []string
			}
		}
	}

	err = testutil.Query(
		`query Test($src: DirectoryID!) {
			directory {
				withDirectory(path: "with-dir", directory: $src) {
					contents(path: "with-dir")
				}
			}
		}`, &res2, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": res.Directory.WithNewFile.WithNewFile.Directory.ID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, res2.Directory.WithDirectory.Contents)
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
