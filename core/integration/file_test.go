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

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
)

type FileSuite struct{}

func TestFile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(FileSuite{})
}

func (FileSuite) TestFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.Directory().
		WithNewFile("some-file", "some-content").
		File("some-file")

	id, err := file.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	contents, err := file.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func (FileSuite) TestContentsLines(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.Directory().
		WithNewFile("some-file", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n").
		File("some-file")

	id, err := file.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	contents, err := file.Contents(ctx, dagger.FileContentsOpts{
		OffsetLines: 5,
		LimitLines:  5,
	})
	require.NoError(t, err)
	require.Equal(t, "6\n7\n8\n9\n10\n", contents)

	contents, err = file.Contents(ctx, dagger.FileContentsOpts{
		OffsetLines: 5,
	})
	require.NoError(t, err)
	require.Equal(t, "6\n7\n8\n9\n10\n11\n12\n", contents)

	contents, err = file.Contents(ctx, dagger.FileContentsOpts{
		LimitLines: 10,
	})
	require.NoError(t, err)
	require.Equal(t, "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", contents)
}

func (FileSuite) TestNewFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.File("some-file", "some-content")

	id, err := file.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	contents, err := file.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func (FileSuite) TestNewFileInvalid(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.File("dir/some-file", "some-content")

	_, err := file.ID(ctx)
	require.ErrorContains(t, err, "not contain a directory")
}

func (FileSuite) TestDirectoryFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.Directory().
		WithNewFile("some-dir/some-file", "some-content").
		Directory("some-dir").
		File("some-file")

	id, err := file.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	contents, err := file.Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func (FileSuite) TestSize(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file := c.Directory().WithNewFile("some-file", "some-content").File("some-file")

	id, err := file.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	size, err := file.Size(ctx)
	require.NoError(t, err)
	require.Equal(t, len("some-content"), size)
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

	t.Run("not found file", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().File("to/file.txt").Name(ctx)
		requireErrOut(t, err, "to/file.txt: no such file or directory")
	})

	t.Run("not found file displays full path in error", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().File("keep/../this").Name(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "keep/../this: no such file or directory")
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

	// regression test for https://github.com/dagger/dagger/issues/11660
	t.Run("contents", func(ctx context.Context, t *testctx.T) {
		f := c.File("test", "hello").WithName("tset")
		s, err := f.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", s)
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
		require.NoError(t, err)
		require.Contains(t, actual, "/some-file")
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
		buf := bytes.Repeat([]byte{'a'}, testFile.size)
		err := os.WriteFile(dest, buf, 0o600)
		require.NoError(t, err)

		// Compute and store hash for generated test data:
		testFiles[i].hash = computeMD5FromReader(bytes.NewReader(buf))
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

func (FileSuite) TestDigest(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("compute file digest", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().WithNewFile("/foo.txt", "Hello, World!")

		digest, err := file.File("/foo.txt").Digest(ctx)
		require.NoError(t, err)
		require.Equal(t, "sha256:8a887cdd3e476c79e1a14a65a6c401673b56071a24561dadb5e152605e72a613", digest)
	})

	t.Run("compute file digest without metadata", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().WithNewFile("/foo.txt", "Hello, World!")

		digest, err := file.File("/foo.txt").Digest(ctx, dagger.FileDigestOpts{ExcludeMetadata: true})
		require.NoError(t, err)
		require.Equal(t, "sha256:042a7d64a581ef2ee983f21058801cc35663b705e6c55f62fa8e0f18ecc70989", digest)
	})

	t.Run("file digest with different metadata should be different", func(ctx context.Context, t *testctx.T) {
		fileWithOverwrittenMetadata := c.Directory().WithNewFile("foo.txt", "Hello, World!", dagger.DirectoryWithNewFileOpts{
			Permissions: 0777,
		}).File("foo.txt")
		fileWithDefaultMetadata := c.Directory().WithNewFile("foo.txt", "Hello, World!").File("foo.txt")

		digestFileWithOverwrittenMetadata, err := fileWithOverwrittenMetadata.Digest(ctx)
		require.NoError(t, err)

		digestFileWithDefaultMetadata, err := fileWithDefaultMetadata.Digest(ctx)
		require.NoError(t, err)

		require.NotEqual(t, digestFileWithOverwrittenMetadata, digestFileWithDefaultMetadata)

		t.Run("except if we exclude them from computation", func(ctx context.Context, t *testctx.T) {
			digestFileWithOverwrittenMetadata, err := fileWithOverwrittenMetadata.Digest(ctx, dagger.FileDigestOpts{ExcludeMetadata: true})
			require.NoError(t, err)

			digestFileWithDefaultMetadata, err := fileWithDefaultMetadata.Digest(ctx, dagger.FileDigestOpts{ExcludeMetadata: true})
			require.NoError(t, err)

			require.Equal(t, digestFileWithOverwrittenMetadata, digestFileWithDefaultMetadata)
		})
	})
}

func (FileSuite) TestSearch(ctx context.Context, t *testctx.T) {
	t.Run("literal search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!\nThis is a test file.\nWorld is great.\nGoodbye, World!").
			File("test.txt")

		results, err := file.Search(ctx, "World")
		require.NoError(t, err)
		require.Len(t, results, 3)

		// Check results
		filePath0, err := results[0].FilePath(ctx)
		require.NoError(t, err)
		lineNumber0, err := results[0].LineNumber(ctx)
		require.NoError(t, err)
		absoluteOffset0, err := results[0].AbsoluteOffset(ctx)
		require.NoError(t, err)
		matchedText0, err := results[0].MatchedLines(ctx)
		require.NoError(t, err)
		submatches0, err := results[0].Submatches(ctx)
		require.NoError(t, err)
		require.Equal(t, "test.txt", filePath0)
		require.Equal(t, 1, lineNumber0)
		require.Contains(t, matchedText0, "Hello, World!\n")
		require.GreaterOrEqual(t, absoluteOffset0, 0)
		require.NotEmpty(t, submatches0)

		filePath1, err := results[1].FilePath(ctx)
		require.NoError(t, err)
		lineNumber1, err := results[1].LineNumber(ctx)
		require.NoError(t, err)
		absoluteOffset1, err := results[1].AbsoluteOffset(ctx)
		require.NoError(t, err)
		matchedText1, err := results[1].MatchedLines(ctx)
		require.NoError(t, err)
		submatches1, err := results[1].Submatches(ctx)
		require.NoError(t, err)
		require.Equal(t, "test.txt", filePath1)
		require.Equal(t, 3, lineNumber1)
		require.Contains(t, matchedText1, "World is great.\n")
		require.GreaterOrEqual(t, absoluteOffset1, 0)
		require.NotEmpty(t, submatches1)

		filePath2, err := results[2].FilePath(ctx)
		require.NoError(t, err)
		lineNumber2, err := results[2].LineNumber(ctx)
		require.NoError(t, err)
		absoluteOffset2, err := results[2].AbsoluteOffset(ctx)
		require.NoError(t, err)
		matchedText2, err := results[2].MatchedLines(ctx)
		require.NoError(t, err)
		submatches2, err := results[2].Submatches(ctx)
		require.NoError(t, err)
		require.Equal(t, "test.txt", filePath2)
		require.Equal(t, 4, lineNumber2)
		require.Equal(t, matchedText2, "Goodbye, World!")
		require.GreaterOrEqual(t, absoluteOffset2, 0)
		require.NotEmpty(t, submatches2)

		// Verify submatch structure for all results
		for i, submatches := range [][]dagger.SearchSubmatch{submatches0, submatches1, submatches2} {
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err, "result %d submatch text", i)
				start, err := submatch.Start(ctx)
				require.NoError(t, err, "result %d submatch start", i)
				end, err := submatch.End(ctx)
				require.NoError(t, err, "result %d submatch end", i)

				require.NotEmpty(t, submatchText, "result %d submatch text should not be empty", i)
				require.GreaterOrEqual(t, start, 0, "result %d submatch start should be non-negative", i)
				require.Greater(t, end, start, "result %d submatch end should be greater than start", i)
				require.Contains(t, submatchText, "World", "result %d submatch should contain 'World'", i)
			}
		}
	})

	t.Run("regex search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("code.go", `package main

import "fmt"

func main() {
	name := "Alice"
	age := 30
	fmt.Printf("Name: %s, Age: %d\n", name, age)
}`).
			File("code.go")

		// Search for variable assignments
		results, err := file.Search(ctx, `\w+ :=`)
		require.NoError(t, err)
		require.NotEmpty(t, results)

		// Check that we have the expected variable assignments
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

			// Verify submatch structure for regex - should match variable assignments
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Greater(t, end, start)
				require.Regexp(t, `\w+ :=`, submatchText)
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check for the specific assignments we expect (may have different formatting)
		require.Contains(t, matches, "code.go:6:\tname := \"Alice\"\n")
		require.Contains(t, matches, "code.go:7:\tage := 30\n")
	})

	t.Run("multiline search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
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
}`).
			File("dir/code.go")

		// Search for variable assignments
		results, err := file.Search(ctx, ":= \"Alice\"\n\tage", dagger.FileSearchOpts{
			Multiline: true,
			Literal:   true,
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

			// Verify submatch structure for multiline literal search
			for _, submatch := range submatches {
				submatchText, err := submatch.Text(ctx)
				require.NoError(t, err)
				start, err := submatch.Start(ctx)
				require.NoError(t, err)
				end, err := submatch.End(ctx)
				require.NoError(t, err)

				require.NotEmpty(t, submatchText)
				require.GreaterOrEqual(t, start, 0)
				require.Greater(t, end, start)
				require.Contains(t, submatchText, ":= \"Alice\"")
				require.Contains(t, submatchText, "age")
			}

			matches = append(matches, fmt.Sprintf("%s:%d:%s", filePath, lineNumber, matchedText))
		}

		// Check for the specific assignments we expect (may have different formatting)
		require.Contains(t, matches, "code.go:6:\tname := \"Alice\"\n\tage := 30\n")
		require.Contains(t, matches, "code.go:12:\tname := \"Alice\"\n\tage := 50\n")
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
}`).
			File("dir/code.go")

		// Search for variable assignments
		results, err := dir.Search(ctx, `:= ".*"\n\s+age`, dagger.FileSearchOpts{
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
		require.Contains(t, matches, "code.go:6:\tname := \"Alice\"\n\tage := 30\n")
		require.Contains(t, matches, "code.go:12:\tname := \"Alice\"\n\tage := 50\n")
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!").
			File("test.txt")

		results, err := file.Search(ctx, "nonexistent")
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("case sensitive search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("test.txt", "Hello\nhello\nHELLO\nHeLLo").
			File("test.txt")

		results, err := file.Search(ctx, "Hello")
		require.NoError(t, err)
		require.Len(t, results, 1)
		lineNumber0, err := results[0].LineNumber(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, lineNumber0)
	})

	t.Run("case insensitive search", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("test.txt", "Hello\nhello\nHELLO\nHeLLo").
			File("test.txt")

		results, err := file.Search(ctx, "hello", dagger.FileSearchOpts{
			Insensitive: true,
		})
		require.NoError(t, err)
		require.Len(t, results, 4)

		// Collect all line numbers to verify we got all matches
		var lineNumbers []int
		for _, result := range results {
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			lineNumbers = append(lineNumbers, lineNumber)
		}
		require.ElementsMatch(t, []int{1, 2, 3, 4}, lineNumbers)
	})

	t.Run("multiline patterns", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("test.py", `def hello():
    print("Hello")

def world():
    print("World")

def hello_world():
    print("Hello, World!")`).
			File("test.py")

		// Search for function definitions
		results, err := file.Search(ctx, `def \w+\(\):`)
		require.NoError(t, err)
		require.Len(t, results, 3)

		lineNumber0, err := results[0].LineNumber(ctx)
		require.NoError(t, err)
		matchedText0, err := results[0].MatchedLines(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, lineNumber0)
		require.Contains(t, matchedText0, "def hello():")

		lineNumber1, err := results[1].LineNumber(ctx)
		require.NoError(t, err)
		matchedText1, err := results[1].MatchedLines(ctx)
		require.NoError(t, err)
		require.Equal(t, 4, lineNumber1)
		require.Contains(t, matchedText1, "def world():")

		lineNumber2, err := results[2].LineNumber(ctx)
		require.NoError(t, err)
		matchedText2, err := results[2].MatchedLines(ctx)
		require.NoError(t, err)
		require.Equal(t, 7, lineNumber2)
		require.Contains(t, matchedText2, "def hello_world():")
	})

	t.Run("large file", func(ctx context.Context, t *testctx.T) {
		// Create a file with many lines
		var content strings.Builder
		for i := 1; i <= 1000; i++ {
			if i%100 == 0 {
				content.WriteString(fmt.Sprintf("Line %d: Special line with MARKER\n", i))
			} else {
				content.WriteString(fmt.Sprintf("Line %d: Regular line\n", i))
			}
		}

		c := connect(ctx, t)

		file := c.Directory().
			WithNewFile("large.txt", content.String()).
			File("large.txt")

		results, err := file.Search(ctx, "MARKER")
		require.NoError(t, err)
		require.Len(t, results, 10)

		// Verify line numbers
		for i, result := range results {
			expectedLine := (i + 1) * 100
			lineNumber, err := result.LineNumber(ctx)
			require.NoError(t, err)
			matchedText, err := result.MatchedLines(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedLine, lineNumber)
			require.Contains(t, matchedText, "MARKER")
		}
	})

	t.Run("file from container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		file := c.Container().
			From(alpineImage).
			File("/etc/alpine-release")

		results, err := file.Search(ctx, "[0-9]+\\.[0-9]+")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		matchedText0, err := results[0].MatchedLines(ctx)
		require.NoError(t, err)
		require.Contains(t, matchedText0, distconsts.AlpineVersion)
	})
}

func (FileSuite) TestSync(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("triggers error", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().File("baz").Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "baz: no such file or directory")

		_, err = c.Container().From(alpineImage).File("/bar").Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "bar: no such file or directory")
	})

	t.Run("allows chaining", func(ctx context.Context, t *testctx.T) {
		file, err := c.Directory().WithNewFile("foo", "bar").File("foo").Sync(ctx)
		require.NoError(t, err)

		contents, err := file.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", contents)
	})
}

func (FileSuite) TestWithReplaced(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("single replacement", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!").
			File("test.txt")

		replaced := file.WithReplaced("World", "Universe")

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Universe!", contents)
	})

	t.Run("single replacement on specified line with multiple matches", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!\nGoodbye, World!\n").
			File("test.txt")

		// Replace only the first occurrence
		replaced := file.WithReplaced("World", "Universe", dagger.FileWithReplacedOpts{
			FirstFrom: 1,
		})

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Universe!\nGoodbye, World!\n", contents)
	})

	t.Run("replace all occurrences", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!\nGoodbye, World!\nAnother World here.").
			File("test.txt")

		// Replace all occurrences
		replaced := file.WithReplaced("World", "Universe", dagger.FileWithReplacedOpts{
			All: true,
		})

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Universe!\nGoodbye, Universe!\nAnother Universe here.", contents)
	})

	t.Run("replace first occurrence after specified line", func(ctx context.Context, t *testctx.T) {
		content := "line 1: World\nline 2: text\nline 3: World\nline 4: World\nline 5: text"
		file := c.Directory().
			WithNewFile("test.txt", content).
			File("test.txt")

		// Replace first occurrence after line 2
		replaced := file.WithReplaced("World", "Universe", dagger.FileWithReplacedOpts{
			FirstFrom: 2,
		})

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "line 1: World\nline 2: text\nline 3: Universe\nline 4: World\nline 5: text", contents)
	})

	t.Run("multiline replacement", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Start\nOld line 1\nOld line 2\nEnd").
			File("test.txt")

		// Replace multiline text
		replaced := file.WithReplaced("Old line 1\nOld line 2", "New single line")

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Start\nNew single line\nEnd", contents)
	})

	t.Run("special characters and regex patterns", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Price: $50.99\nTotal: $100.50").
			File("test.txt")

		// Replace literal dollar signs and dots (not regex)
		replaced := file.WithReplaced("$50.99", "$75.25")

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Price: $75.25\nTotal: $100.50", contents)
	})

	t.Run("empty replacement", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Remove this text and keep the rest").
			File("test.txt")

		// Remove text by replacing with empty string
		replaced := file.WithReplaced("Remove this text and ", "")

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep the rest", contents)
	})

	t.Run("error on no matches", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!").
			File("test.txt")

		// Should error when no matches found - error will surface on Contents() call
		replaced := file.WithReplaced("NotFound", "Replacement")
		_, err := replaced.Contents(ctx)
		require.Error(t, err)
		require.Contains(t, strings.ToLower(err.Error()), "not found")
	})

	t.Run("error on multiple matches without all flag", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "World appears here and World appears there").
			File("test.txt")

		// Should error when multiple matches exist and all=false (default) - error will surface on Contents() call
		replaced := file.WithReplaced("World", "Universe")
		_, err := replaced.Contents(ctx)
		require.Error(t, err)
		require.Contains(t, strings.ToLower(err.Error()), "multiple")
	})

	t.Run("first occurrence after non-existent line", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "line 1\nline 2").
			File("test.txt")

		// Should error when firstAfter points beyond file length - error will surface on Contents() call
		replaced := file.WithReplaced("line", "LINE", dagger.FileWithReplacedOpts{
			FirstFrom: 10,
		})
		_, err := replaced.Contents(ctx)
		require.Error(t, err)
	})

	t.Run("preserve file attributes", func(ctx context.Context, t *testctx.T) {
		originalFile := c.Directory().
			WithNewFile("test.txt", "Original content").
			File("test.txt")

		// Get original file name
		originalName, err := originalFile.Name(ctx)
		require.NoError(t, err)

		// Replace content and verify file attributes are preserved
		replaced := originalFile.WithReplaced("Original", "Modified")

		newName, err := replaced.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, originalName, newName)

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Modified content", contents)
	})

	t.Run("chaining with other operations", func(ctx context.Context, t *testctx.T) {
		replaced := c.Directory().
			WithNewFile("chain.txt", "Step 1: initial").
			File("chain.txt").
			WithReplaced("initial", "replaced").
			WithReplaced("Step 1", "Step 2")

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Step 2: replaced", contents)
	})

	t.Run("all=true with no matches is no-op", func(ctx context.Context, t *testctx.T) {
		file := c.Directory().
			WithNewFile("test.txt", "Hello, World!").
			File("test.txt")

		// Should not error when no matches found with all=true (should be a no-op)
		replaced := file.WithReplaced("NotFound", "Replacement", dagger.FileWithReplacedOpts{
			All: true,
		})

		// The file should be returned unchanged
		_, err := replaced.ID(ctx)
		require.NoError(t, err)

		contents, err := replaced.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, World!", contents) // Content should be unchanged
	})
}

func (FileSuite) TestFileAsJSON(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("it converts json file contents to JSON", func(ctx context.Context, t *testctx.T) {
		jsonValue, err := c.Directory().
			WithNewFile("test.json", `{ "somekey": "somevalue" }`).
			File("test.json").
			AsJSON().
			Field([]string{"somekey"}).
			AsString(ctx)

		require.NoError(t, err)
		require.Equal(t, "somevalue", jsonValue)
	})

	t.Run("it returns error with non-json", func(ctx context.Context, t *testctx.T) {
		_, err := c.Directory().
			WithNewFile("test.txt", `this is not json`).
			File("test.txt").
			AsJSON().
			Field([]string{"sdk", "source"}).
			AsString(ctx)

		require.Error(t, err)
		require.ErrorContains(t, err, "invalid JSON")
	})
}

func (FileSuite) TestFileRespectsSymlinks(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("root-level", func(ctx context.Context, t *testctx.T) {
		s, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "echo -n 'important' > data && ln -s data d"}).
			File("d").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "important", s)
	})
	t.Run("target-in-subdir", func(ctx context.Context, t *testctx.T) {
		s, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "mkdir data-store && echo -n 'important' > data-store/data && ln -s data-store/data d"}).
			File("d").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "important", s)
	})
	t.Run("target-in-parent-dir", func(ctx context.Context, t *testctx.T) {
		d := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "mkdir subdir && echo -n 'important' > data && cd subdir && ln -s ../data d"})

		s, err := d.
			File("subdir/d").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "important", s)

		s, err = d.
			Directory("subdir").
			File("d").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "important", s)
	})
}

// regression test for https://github.com/dagger/dagger/issues/11552
func (FileSuite) TestFileCachingContents(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	c := connect(ctx, t, dagger.WithWorkdir(wd))

	var eg errgroup.Group
	startCh := make(chan struct{})
	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("file%d.txt", i)
		contents := fmt.Sprintf("%d", i)
		err := os.WriteFile(filepath.Join(wd, filename), []byte(contents), 0o600)
		require.NoError(t, err)

		eg.Go(func() error {
			<-startCh
			file := c.Host().Directory(".").File(filename)

			actualContents, err := c.Directory().
				WithFile("the-file", file).
				File("the-file").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, contents, actualContents)
			return nil
		})
	}
	close(startCh)
	require.NoError(t, eg.Wait())
}
