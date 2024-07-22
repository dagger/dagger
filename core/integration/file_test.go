package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

type FileSuite struct{}

func TestFile(t *testing.T) {
	testctx.Run(testCtx, t, FileSuite{}, Middleware()...)
}

func (FileSuite) TestFile(ctx context.Context, t *testctx.T) {
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

	err := testutil.Query(t,
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

func (FileSuite) TestDirectoryFile(ctx context.Context, t *testctx.T) {
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

	err := testutil.Query(t,
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

func (FileSuite) TestSize(ctx context.Context, t *testctx.T) {
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

	err := testutil.Query(t,
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

func (FileSuite) TestName(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()

	c := connect(ctx, t, dagger.WithWorkdir(wd))

	t.Run("new file", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().WithNewFile("/foo/bar", "content1").File("foo/bar")

		name, err := file.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", name)
	})

	t.Run("container file", func(ctx context.Context, t *testctx.T) {
		file := c.Container().From(alpineImage).File("/etc/alpine-release")

		name, err := file.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "alpine-release", name)
	})

	t.Run("container file in dir", func(ctx context.Context, t *testctx.T) {
		file := c.Container().From(alpineImage).Directory("/etc").File("/alpine-release")

		name, err := file.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "alpine-release", name)
	})

	t.Run("host file", func(ctx context.Context, t *testctx.T) {
		err := os.WriteFile(filepath.Join(wd, "file.txt"), []byte{}, 0o600)
		require.NoError(t, err)

		name, err := c.Host().File("file.txt").Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "file.txt", name)
	})

	t.Run("host file in dir", func(ctx context.Context, t *testctx.T) {
		err := os.MkdirAll(filepath.Join(wd, "path/to/"), 0o700)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(wd, "path/to/file.txt"), []byte{}, 0o600)
		require.NoError(t, err)

		name, err := c.Host().Directory("path").File("to/file.txt").Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "file.txt", name)
	})
}

func (FileSuite) TestWithName(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("new file with new name", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().WithNewFile("/foo/bar", "content").File("foo/bar")

		newFile := file.WithName("baz")

		name, err := newFile.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "baz", name)
	})

	t.Run("mounted file with new name", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().WithNewFile("/foo/bar", "content").File("foo/bar")

		newFile := file.WithName("baz")

		mountedFile := c.Directory().WithFile("", newFile).File("baz")

		mountedFileName, err := mountedFile.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "baz", mountedFileName)

		mountedFileNameContent, err := mountedFile.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "content", mountedFileNameContent)
	})
}

func (FileSuite) TestExport(ctx context.Context, t *testctx.T) {
	file := func(c *dagger.Client) *dagger.File {
		return c.Container().From(alpineImage).File("/etc/alpine-release")
	}

	t.Run("to absolute path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		targetDir := t.TempDir()
		dest := filepath.Join(targetDir, "some-file")

		actual, err := file(c).Export(ctx, dest)
		require.NoError(t, err)
		require.Equal(t, dest, actual)

		contents, err := os.ReadFile(dest)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(contents), distconsts.AlpineVersion), string(contents))

		entries, err := ls(targetDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})

	t.Run("to relative path", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		c := connect(ctx, t, dagger.WithWorkdir(wd))
		actual, err := file(c).Export(ctx, "some-file")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "some-file"), actual)

		contents, err := os.ReadFile(filepath.Join(wd, "some-file"))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(contents), distconsts.AlpineVersion), string(contents))

		entries, err := ls(wd)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})

	t.Run("to path in outer dir", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		c := connect(ctx, t, dagger.WithWorkdir(wd))
		actual, err := file(c).Export(ctx, "../some-file")
		require.Error(t, err)
		require.Empty(t, actual)
	})

	t.Run("to absolute dir", func(ctx context.Context, t *testctx.T) {
		targetDir := t.TempDir()
		c := connect(ctx, t)
		actual, err := file(c).Export(ctx, targetDir)
		require.Error(t, err)
		require.Empty(t, actual)
	})

	t.Run("to workdir", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		c := connect(ctx, t, dagger.WithWorkdir(wd))
		actual, err := file(c).Export(ctx, ".")
		require.Error(t, err)
		require.Empty(t, actual)
	})

	t.Run("file under subdir", func(ctx context.Context, t *testctx.T) {
		targetDir := t.TempDir()
		c := connect(ctx, t)
		dir := c.Directory().
			WithNewFile("/file", "content1").
			WithNewFile("/subdir/file", "content2")
		file := dir.File("/subdir/file")

		dest := filepath.Join(targetDir, "da-file")
		_, err := file.Export(ctx, dest)
		require.NoError(t, err)
		contents, err := os.ReadFile(dest)
		require.NoError(t, err)
		require.Equal(t, "content2", string(contents))

		dir = dir.Directory("/subdir")
		file = dir.File("file")

		dest = filepath.Join(targetDir, "da-file-2")
		_, err = file.Export(ctx, dest)
		require.NoError(t, err)
		contents, err = os.ReadFile(dest)
		require.NoError(t, err)
		require.Equal(t, "content2", string(contents))
	})

	t.Run("file larger than max chunk size", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		c := connect(ctx, t, dagger.WithWorkdir(wd))
		maxChunkSize := buildkit.MaxFileContentsChunkSize
		fileSizeBytes := maxChunkSize*4 + 1 // +1 so it's not an exact number of chunks, to ensure we cover that case

		file := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", fmt.Sprintf("dd if=/dev/zero of=/file bs=%d count=1", fileSizeBytes)}).
			File("/file")

		dt, err := file.Contents(ctx)
		require.NoError(t, err)
		require.EqualValues(t, fileSizeBytes, len(dt))

		_, err = file.Export(ctx, "some-pretty-big-file")
		require.NoError(t, err)

		stat, err := os.Stat(filepath.Join(wd, "some-pretty-big-file"))
		require.NoError(t, err)
		require.EqualValues(t, fileSizeBytes, stat.Size())
	})

	t.Run("file permissions are retained", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()
		c := connect(ctx, t, dagger.WithWorkdir(wd))
		_, err := c.Directory().WithNewFile("/file", "#!/bin/sh\necho hello", dagger.DirectoryWithNewFileOpts{
			Permissions: 0o744,
		}).File("/file").Export(ctx, "some-executable-file")
		require.NoError(t, err)
		stat, err := os.Stat(filepath.Join(wd, "some-executable-file"))
		require.NoError(t, err)
		require.EqualValues(t, 0o744, stat.Mode().Perm())
	})
}

func (FileSuite) TestWithTimestamps(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	reallyImportantTime := time.Date(1985, 10, 26, 8, 15, 0, 0, time.UTC)

	file := c.Directory().
		WithNewFile("sub-dir/sub-file", "sub-content").
		File("sub-dir/sub-file").
		WithTimestamps(int(reallyImportantTime.Unix()))

	ls, err := c.Container().
		From(alpineImage).
		WithMountedFile("/file", file).
		WithEnvVariable("RANDOM", identity.NewID()).
		WithExec([]string{"stat", "/file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, ls, "Access: 1985-10-26 08:15:00.000000000 +0000")
	require.Contains(t, ls, "Modify: 1985-10-26 08:15:00.000000000 +0000")
}

func (FileSuite) TestContents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set three types of file sizes for test data,
	// the third one uses a size larger than the max chunk size:
	testFiles := []struct {
		size int
		hash string
	}{
		{size: buildkit.MaxFileContentsChunkSize / 2},
		{size: buildkit.MaxFileContentsChunkSize},
		{size: buildkit.MaxFileContentsChunkSize * 2},
		{size: buildkit.MaxFileContentsSize + 1},
	}
	tempDir := t.TempDir()
	for i, testFile := range testFiles {
		filename := strconv.Itoa(i)
		dest := filepath.Join(tempDir, filename)
		var buf bytes.Buffer
		for i := 0; i < testFile.size; i++ {
			buf.WriteByte('a')
		}
		err := os.WriteFile(dest, buf.Bytes(), 0o600)
		require.NoError(t, err)

		// Compute and store hash for generated test data:
		testFiles[i].hash = computeMD5FromReader(&buf)
	}

	hostDir := c.Host().Directory(tempDir)
	alpine := c.Container().
		From(alpineImage).WithDirectory(".", hostDir)

	// Grab file contents and compare hashes to validate integrity:
	for i, testFile := range testFiles {
		filename := strconv.Itoa(i)
		contents, err := alpine.File(filename).Contents(ctx)

		// Assert error on larger files:
		if testFile.size > buildkit.MaxFileContentsSize {
			require.Error(t, err)
			continue
		}

		require.NoError(t, err)
		contentsHash := computeMD5FromReader(strings.NewReader(contents))
		require.Equal(t, testFile.hash, contentsHash)
	}
}

func (FileSuite) TestSync(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("triggers error", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().File("baz").Sync(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such file")

		_, err = c.Container().From(alpineImage).File("/bar").Sync(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such file")
	})

	t.Run("allows chaining", func(ctx context.Context, t *testctx.T) {
		file, err := c.Directory().WithNewFile("foo", "bar").File("foo").Sync(ctx)
		require.NoError(t, err)

		contents, err := file.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", contents)
	})
}
