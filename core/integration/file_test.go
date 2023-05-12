package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
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

	file := c.Container().From("alpine:3.16.2").File("/etc/alpine-release")

	t.Run("to absolute path", func(t *testing.T) {
		dest := filepath.Join(targetDir, "some-file")

		ok, err := file.Export(ctx, dest)
		require.NoError(t, err)
		require.True(t, ok)

		contents, err := os.ReadFile(dest)
		require.NoError(t, err)
		require.Equal(t, "3.16.2\n", string(contents))

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
		require.Equal(t, "3.16.2\n", string(contents))

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
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	reallyImportantTime := time.Date(1985, 10, 26, 8, 15, 0, 0, time.UTC)

	file := c.Directory().
		WithNewFile("sub-dir/sub-file", "sub-content").
		File("sub-dir/sub-file").
		WithTimestamps(int(reallyImportantTime.Unix()))

	ls, err := c.Container().
		From("alpine:3.16.2").
		WithMountedFile("/file", file).
		WithEnvVariable("RANDOM", identity.NewID()).
		WithExec([]string{"stat", "/file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, ls, "Access: 1985-10-26 08:15:00.000000000 +0000")
	require.Contains(t, ls, "Modify: 1985-10-26 08:15:00.000000000 +0000")
}

func TestFileContents(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	// Set three types of file sizes for test data,
	// the third one uses a size larger than the max chunk size:
	testFiles := []struct {
		size int
		hash string
	}{
		{size: core.MaxFileContentsChunkSize / 2},
		{size: core.MaxFileContentsChunkSize},
		{size: core.MaxFileContentsChunkSize * 2},
		{size: core.MaxFileContentsSize + 1},
	}
	tempDir := t.TempDir()
	for i, testFile := range testFiles {
		filename := strconv.Itoa(i)
		dest := filepath.Join(tempDir, filename)
		var buf bytes.Buffer
		for i := 0; i < testFile.size; i++ {
			buf.WriteByte('a')
		}
		err := os.WriteFile(dest, buf.Bytes(), 0600)
		require.NoError(t, err)

		// Compute and store hash for generated test data:
		testFiles[i].hash = computeMD5FromReader(&buf)
	}

	hostDir := c.Host().Directory(tempDir)
	alpine := c.Container().
		From("alpine:3.16.2").WithDirectory(".", hostDir)

	// Grab file contents and compare hashes to validate integrity:
	for i, testFile := range testFiles {
		filename := strconv.Itoa(i)
		contents, err := alpine.File(filename).Contents(ctx)

		// Assert error on larger files:
		if testFile.size > core.MaxFileContentsSize {
			require.Error(t, err)
			continue
		}

		require.NoError(t, err)
		contentsHash := computeMD5FromReader(strings.NewReader(contents))
		require.Equal(t, testFile.hash, contentsHash)
	}
}
