package core

import (
	"context"
	"regexp"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
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
				entries
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Directory.Entries)
}

func TestScratchDirectory(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()
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
	defer c.Close()

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
			WithNewFile("some-file", "some content", dagger.DirectoryWithNewFileOpts{Permissions: 0444}).
			WithNewDirectory("some-dir", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0444}).
			WithNewFile("some-dir/sub-file", "sub-content", dagger.DirectoryWithNewFileOpts{Permissions: 0444})
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
	defer c.Close()

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
		WithNewFile("some-file", "some-content").
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

	t.Run("respects permissions", func(t *testing.T) {
		dir := c.Directory().
			WithNewFile(
				"file-with-permissions",
				"this should have rwxrwxrwx permissions",
				dagger.DirectoryWithNewFileOpts{Permissions: 0777})

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

func TestDirectoryWithTimestamps(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	reallyImportantTime := time.Date(1985, 10, 26, 8, 15, 0, 0, time.UTC)

	dir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("sub-dir/sub-file", "sub-content").
		WithTimestamps(int(reallyImportantTime.Unix()))

	t.Run("changes file and directory timestamps recursively", func(t *testing.T) {
		ls, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/dir", dir).
			WithEnvVariable("RANDOM", identity.NewID()).
			WithExec([]string{"sh", "-c", "ls -al /dir && ls -al /dir/sub-dir"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+1 root\s+root\s+\d+ Oct 26  1985 some-file`), ls)
		require.Regexp(t, regexp.MustCompile(`drwxr-xr-x\s+2 root\s+root\s+\d+ Oct 26  1985 sub-dir`), ls)
		require.Regexp(t, regexp.MustCompile(`-rw-r--r--\s+1 root\s+root\s+\d+ Oct 26  1985 sub-file`), ls)
		// require.Contains(t, ls, "-rw-r--r--    1 root     root            12 Oct 26  1985 some-file")
		// require.Contains(t, ls, "drwxr-xr-x    2 root     root          4096 Oct 26  1985 sub-dir")
		// require.Contains(t, ls, "-rw-r--r--    1 root     root            11 Oct 26  1985 sub-file")
	})

	t.Run("results in stable tar archiving", func(t *testing.T) {
		content, err := c.Container().
			From("alpine:3.16.2").
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
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	dir1 := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content")

	entries, err := dir1.
		WithoutDirectory("some-dir").
		Entries(ctx)

	require.NoError(t, err)
	require.Equal(t, []string{"some-file"}, entries)

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

func TestDirectoryDockerBuild(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

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

		env, err := src.DockerBuild().WithExec([]string{}).Stdout(ctx)
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
		}).WithExec([]string{}).Stdout(ctx)
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

		env, err := sub.DockerBuild().WithExec([]string{}).Stdout(ctx)
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
		}).WithExec([]string{}).Stdout(ctx)
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

		env, err := c.Container().Build(src).WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		env, err = c.Container().Build(src, dagger.ContainerBuildOpts{BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}}}).WithExec([]string{}).Stdout(ctx)
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

		output, err := src.DockerBuild().WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		output, err = src.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Target: "stage1",
		}).WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})
}
