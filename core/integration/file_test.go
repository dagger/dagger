package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/image"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestFile(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID       core.FileID
					Contents string
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
						contents
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.File.ID)
	require.Equal(t, "some-content", res.Directory.WithNewFile.File.Contents)
}

func TestDirectoryFile(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				Directory struct {
					File struct {
						ID       core.FileID
						Contents string
					}
				}
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-dir/some-file", contents: "some-content") {
					directory(path: "some-dir") {
						file(path: "some-file") {
							id
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.Directory.File.ID)
	require.Equal(t, "some-content", res.Directory.WithNewFile.Directory.File.Contents)
}

func TestFileSize(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID   core.FileID
					Size int
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
						size
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Directory.WithNewFile.File.ID)
	require.Equal(t, len("some-content"), res.Directory.WithNewFile.File.Size)
}

func TestFileExport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	wd := t.TempDir()
	targetDir := t.TempDir()

	c, err := dagger.Connect(ctx, dagger.WithWorkdir(wd))
	require.NoError(t, err)
	defer c.Close()

	file := c.Container().From(image.Alpine).File("/etc/alpine-release")

	t.Run("to absolute path", func(t *testing.T) {
		dest := filepath.Join(targetDir, "some-file")

		ok, err := file.Export(ctx, dest)
		require.NoError(t, err)
		require.True(t, ok)

		contents, err := os.ReadFile(dest)
		require.NoError(t, err)
		require.Equal(t, "3.17.2\n", string(contents))

		entries, err := ls(targetDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})

	t.Run("to relative path", func(t *testing.T) {
		ok, err := file.Export(ctx, "some-file")
		require.NoError(t, err)
		require.True(t, ok)

		contents, err := os.ReadFile(filepath.Join(wd, "some-file"))
		require.NoError(t, err)
		require.Equal(t, "3.17.2\n", string(contents))

		entries, err := ls(wd)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})

	t.Run("to path in outer dir", func(t *testing.T) {
		ok, err := file.Export(ctx, "../some-file")
		require.Error(t, err)
		require.False(t, ok)
	})

	t.Run("to absolute dir", func(t *testing.T) {
		ok, err := file.Export(ctx, targetDir)
		require.Error(t, err)
		require.False(t, ok)
	})

	t.Run("to workdir", func(t *testing.T) {
		ok, err := file.Export(ctx, ".")
		require.Error(t, err)
		require.False(t, ok)
	})
}

func TestFileWithTimestamps(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	reallyImportantTime := time.Date(1985, 10, 26, 8, 15, 0, 0, time.UTC)

	file := c.Directory().
		WithNewFile("sub-dir/sub-file", "sub-content").
		File("sub-dir/sub-file").
		WithTimestamps(int(reallyImportantTime.Unix()))

	ls, err := c.Container().
		From(image.Alpine).
		WithMountedFile("/file", file).
		WithEnvVariable("RANDOM", identity.NewID()).
		WithExec([]string{"stat", "/file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, ls, "Access: 1985-10-26 08:15:00.000000000 +0000")
	require.Contains(t, ls, "Modify: 1985-10-26 08:15:00.000000000 +0000")
}
