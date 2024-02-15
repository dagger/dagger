package core

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
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
				entries
			}
		}`, &res, nil, dagger.WithLogOutput(os.Stderr))
	require.NoError(t, err)
	require.Empty(t, res.Directory.Entries)
}

func TestScratchDirectory(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	_, err := c.Container().Directory("/").Entries(ctx)
	require.NoError(t, err)
	// require.ErrorContains(t, err, "no such file or directory")
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

	t.Run("copies directory contents to .", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, entries)
	})

	t.Run("respects permissions", func(t *testing.T) {
		dir := c.Directory().
			WithNewFile("some-file", "some content", dagger.DirectoryWithNewFileOpts{Permissions: 0o444}).
			WithNewDirectory("some-dir", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o444}).
			WithNewFile("some-dir/sub-file", "sub-content", dagger.DirectoryWithNewFileOpts{Permissions: 0o444})
		ctr := c.Container().From("alpine").WithDirectory("/permissions-test", dir)

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

func TestDirectoryWithDirectoryIncludeExclude(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	dir := c.Directory().
		WithNewFile("a.txt", "").
		WithNewFile("b.txt", "").
		WithNewFile("c.txt.rar", "").
		WithNewFile("subdir/d.txt", "").
		WithNewFile("subdir/e.txt", "").
		WithNewFile("subdir/f.txt.rar", "")

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

	t.Run("exclude works on directory", func(t *testing.T) {
		entries, err := c.Directory().WithDirectory(".", dir, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{"subdir"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "c.txt.rar"}, entries)
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
	t.Parallel()
	c, ctx := connect(t)

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
	t.Parallel()
	c, ctx := connect(t)

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

	t.Run("respects permissions", func(t *testing.T) {
		dir := c.Directory().
			WithNewFile(
				"file-with-permissions",
				"this should have rwxrwxrwx permissions",
				dagger.DirectoryWithNewFileOpts{Permissions: 0o777})

		ctr := c.Container().From("alpine").WithDirectory("/permissions-test", dir)

		stdout, err := ctr.WithExec([]string{"ls", "-l", "/permissions-test/file-with-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "rwxrwxrwx")

		dir2 := c.Directory().
			WithNewFile(
				"file-with-permissions",
				"this should have rw-r--r-- permissions")
		ctr2 := c.Container().From("alpine").WithDirectory("/permissions-test", dir2)
		stdout2, err := ctr2.WithExec([]string{"ls", "-l", "/permissions-test/file-with-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout2, "rw-r--r--")
	})
}

func TestDirectoryWithFiles(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	file1 := c.Directory().
		WithNewFile("first-file", "file1 content").
		File("first-file")
	file2 := c.Directory().
		WithNewFile("second-file", "file2 content").
		File("second-file")
	files := []*dagger.File{file1, file2}
	dir := c.Directory().
		WithFiles("/", files)

	// check file1 contents
	content, err := dir.File("/first-file").Contents(ctx)
	require.Equal(t, "file1 content", content)
	require.NoError(t, err)

	// check file2 contents
	content, err = dir.File("/second-file").Contents(ctx)
	require.Equal(t, "file2 content", content)
	require.NoError(t, err)

	_, err = dir.File("/some-other-file").Contents(ctx)
	require.Error(t, err)

	// test sub directory
	subDir := c.Directory().
		WithFiles("/a/b/c", files)
	content, err = subDir.File("/a/b/c/first-file").Contents(ctx)
	require.Equal(t, "file1 content", content)
	require.NoError(t, err)

	t.Run("respects permissions", func(t *testing.T) {
		file1 := c.Directory().
			WithNewFile("file-set-permissions", "this should have rwxrwxrwx permissions", dagger.DirectoryWithNewFileOpts{Permissions: 0o777}).
			File("file-set-permissions")
		file2 := c.Directory().
			WithNewFile("file-default-permissions", "this should have rw-r--r-- permissions").
			File("file-default-permissions")
		files := []*dagger.File{file1, file2}
		dir := c.Directory().
			WithFiles("/", files)

		ctr := c.Container().From("alpine").WithDirectory("/permissions-test", dir)

		stdout, err := ctr.WithExec([]string{"ls", "-l", "/permissions-test/file-set-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "rwxrwxrwx")

		ctr2 := c.Container().From("alpine").WithDirectory("/permissions-test", dir)
		stdout2, err := ctr2.WithExec([]string{"ls", "-l", "/permissions-test/file-default-permissions"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout2, "rw-r--r--")
	})
}

func TestDirectoryWithTimestamps(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

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

	t.Run("changes file and directory timestamps recursively", func(t *testing.T) {
		ls, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/dir", dir).
			WithEnvVariable("RANDOM", identity.NewID()).
			WithExec([]string{"sh", "-c", "ls -al /dir && ls -al /dir/sub-dir"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+1 root\s+root\s+\d+ Oct 26  1985 some-file`), ls)
		require.Regexp(t, regexp.MustCompile(`drwxr-xr-x\s+2 root\s+root\s+\d+ Oct 26  1985 sub-dir`), ls)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+1 root\s+root\s+\d+ Oct 26  1985 sub-file`), ls)
	})

	t.Run("results in stable tar archiving", func(t *testing.T) {
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

func TestDirectoryWithoutDirectoryWithoutFile(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

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
	require.Equal(t, []string{"some-dir", "some-file"}, entries)

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
	require.Equal(t, []string{"a", "b", "c", "foo.txt"}, entries)

	entries, err = dir.
		WithoutDirectory("a").
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"b", "c", "foo.txt"}, entries)

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
	require.Equal(t, []string{"b1", "foo.txt"}, entries)

	// Test WithoutFile
	filesDir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		WithoutFile("some-file")

	entries, err = filesDir.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"some-dir"}, entries)

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

	wd := t.TempDir()
	dest := t.TempDir()

	c, ctx := connect(t, dagger.WithWorkdir(wd))

	dir := c.Container().From(alpineImage).Directory("/etc/profile.d")

	t.Run("to absolute dir", func(t *testing.T) {
		ok, err := dir.Export(ctx, dest)
		require.NoError(t, err)
		require.True(t, ok)

		entries, err := ls(dest)
		require.NoError(t, err)
		require.Equal(t, []string{"20locale.sh", "README", "color_prompt.sh.disabled"}, entries)
	})

	t.Run("to workdir", func(t *testing.T) {
		ok, err := dir.Export(ctx, ".")
		require.NoError(t, err)
		require.True(t, ok)

		entries, err := ls(wd)
		require.NoError(t, err)
		require.Equal(t, []string{"20locale.sh", "README", "color_prompt.sh.disabled"}, entries)
	})

	t.Run("to outer dir", func(t *testing.T) {
		ok, err := dir.Export(ctx, "../")
		require.Error(t, err)
		require.False(t, ok)
	})
}

func TestDirectoryDockerBuild(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

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

	t.Run("default Dockerfile location", func(t *testing.T) {
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

		env, err := src.DockerBuild().Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("custom Dockerfile location", func(t *testing.T) {
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
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(t *testing.T) {
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

		env, err := sub.DockerBuild().Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(t *testing.T) {
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
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with build args", func(t *testing.T) {
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

		env, err := src.DockerBuild().Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		env, err = src.DockerBuild(dagger.DirectoryDockerBuildOpts{BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}}}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=barbar\n")
	})

	t.Run("with target", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine AS base
CMD echo "base"

FROM base AS stage1
CMD echo "stage1"

FROM base AS stage2
CMD echo "stage2"
`)

		output, err := src.DockerBuild().Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		output, err = src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Target: "stage1",
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})

	t.Run("with build secrets", func(t *testing.T) {
		sec := c.SetSecret("my-secret", "barbar")

		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret cp /run/secrets/my-secret  /secret
CMD cat /secret
`)

		stdout, err := src.DockerBuild(dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "***")
	})
}

func TestDirectoryWithNewFileExceedingLength(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "bhhivbryticrxrjssjtflvkxjsqyltawpjexixdfnzoxpoxtdheuhvqalteblsqspfeblfaayvrxejknhpezrxtwxmqzaxgtjdupwnwyosqbvypdwroozcyplzhdxrrvhpskmocmgtdnoeaecbyvpovpwdwpytdxwwedueyaxytxsnnnsfpfjtnlkrxwxtcikcocnkobvdxdqpbafqhmidqbrnhxlxqynesyijgkfepokrnsfqneixfvgsdy.txt", contents: "some-content") {
					id
				}
			}
		}`, &res, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "File name length exceeds the maximum supported 255 characters")
}

func TestDirectoryWithFileExceedingLength(t *testing.T) {
	t.Parallel()

	var res struct {
		Directory struct {
			WithNewFile struct {
				file struct {
					ID core.DirectoryID
				}
			}
		}
	}

	err := testutil.Query(
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
	require.Contains(t, err.Error(), "File name length exceeds the maximum supported 255 characters")
}

func TestDirectoryDirectMerge(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	getDirAndInodes := func(t *testing.T, fileNames ...string) (*dagger.Directory, []string) {
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

func TestDirectoryFallbackMerge(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	t.Run("dest path same as src selector", func(t *testing.T) {
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

func TestDirectorySync(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("empty", func(t *testing.T) {
		dir, err := c.Directory().Sync(ctx)
		require.NoError(t, err)

		entries, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("triggers error", func(t *testing.T) {
		_, err := c.Directory().Directory("/foo").Sync(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such file or directory")

		_, err = c.Container().From(alpineImage).Directory("/bar").Sync(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such file or directory")
	})

	t.Run("allows chaining", func(t *testing.T) {
		dir, err := c.Directory().WithNewFile("foo", "bar").Sync(ctx)
		require.NoError(t, err)

		entries, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"foo"}, entries)
	})
}

func TestDirectoryGlob(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

	t.Run("include only markdown", func(t *testing.T) {
		entries, err := srcDir.Glob(ctx, "*.md")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{"test.md"})
	})

	t.Run("recursive listing", func(t *testing.T) {
		entries, err := srcDir.Glob(ctx, "**/*")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{
			"func.go", "main.go", "test.md", "foo.txt", "README.txt",
			"subdir", "subdir/foo.txt", "subdir/README.md",
			"subdir2", "subdir2/subsubdir", "subdir2/baz.txt", "subdir2/TESTING.md",
			"subdir/subsubdir", "subdir/subsubdir/package.json",
			"subdir/subsubdir/index.mts", "subdir/subsubdir/JS.md",
		})
	})

	t.Run("recursive that include only markdown", func(t *testing.T) {
		entries, err := srcDir.Glob(ctx, "**/*.md")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{
			"test.md", "subdir/README.md",
			"subdir2/TESTING.md", "subdir/subsubdir/JS.md",
		})
	})

	t.Run("recursive with directories in the pattern", func(t *testing.T) {
		srcDir := c.Directory().
			WithNewFile("foo/bar.md/x.md", "").
			WithNewFile("foo/bar.md/y.go", "")

		entries, err := srcDir.Glob(ctx, "**/*.md")

		require.NoError(t, err)
		require.ElementsMatch(t, entries, []string{"foo/bar.md", "foo/bar.md/x.md"})
	})
}
