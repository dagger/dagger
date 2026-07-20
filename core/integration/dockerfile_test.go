package core

// These tests cover building containers from Dockerfiles through the Dagger API.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type DockerfileSuite struct{}

func TestDockerfile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DockerfileSuite{})
}

func (DockerfileSuite) TestDockerBuild(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := c.Container().
		From(golangImage).
		WithWorkdir("/src").
		WithExec([]string{"go", "mod", "init", "hello"}).
		WithNewFile("main.go",
			`package main
import "fmt"
import "os"
func main() {
	for _, env := range os.Environ() {
		fmt.Println(env)
	}
}`).
		Directory(".")
	baseDir := contextDir

	t.Run("default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = docker/dockerfile:1
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with old syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = docker/dockerfile:1.7
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with unknown syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = example.com/custom/dockerfile:1
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		_, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `syntax frontend "example.com/custom/dockerfile:1" is unsupported`)
	})

	t.Run("custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		opts := dagger.DirectoryDockerBuildOpts{Dockerfile: "subdir/Dockerfile.whee"}
		env, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		sub := c.Directory().WithDirectory("subcontext", dir).Directory("subcontext")
		env, err := sub.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		sub := c.Directory().WithDirectory("subcontext", dir).Directory("subcontext")
		opts := dagger.DirectoryDockerBuildOpts{Dockerfile: "subdir/Dockerfile.whee"}
		env, err := sub.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("copy-directory-to-explicit-destination-path", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("SHA256SUMS.d/buildkit-v0.1", "sha256-checksum-line").
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
COPY ./SHA256SUMS.d/ /SHA256SUMS.d
RUN test -f /SHA256SUMS.d/buildkit-v0.1
CMD ["cat", "/SHA256SUMS.d/buildkit-v0.1"]
`)

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "sha256-checksum-line", out)
	})

	t.Run("add-http-with-checksum-success", func(ctx context.Context, t *testctx.T) {
		t.Skip("TODO: enable once llbtodagger supports HTTP checksum enforcement in source(http) conversion")

		const sourceURL = "https://raw.githubusercontent.com/octocat/Hello-World/master/README"

		sourceContents, err := c.HTTP(sourceURL).Contents(ctx)
		require.NoError(t, err)
		expected := digest.FromString(sourceContents).String()

		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD --checksum=%s %s /downloads/README
CMD ["cat", "/downloads/README"]
`, alpineImage, expected, sourceURL))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, sourceContents, out)
	})

	t.Run("add-http-with-checksum-mismatch", func(ctx context.Context, t *testctx.T) {
		t.Skip("TODO: enable once llbtodagger supports HTTP checksum enforcement in source(http) conversion")

		const sourceURL = "https://raw.githubusercontent.com/octocat/Hello-World/master/README"

		wrong := digest.FromString("wrong-checksum").String()
		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD --checksum=%s %s /downloads/README
`, alpineImage, wrong, sourceURL))

		_, err := dir.DockerBuild().Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "checksum mismatch")
	})

	t.Run("add-git-query-string-variants", func(ctx context.Context, t *testctx.T) {
		const branchURL = "https://github.com/octocat/Hello-World.git?branch=master"
		const refURL = "https://github.com/octocat/Hello-World.git?ref=master"
		const fragmentURL = "https://github.com/octocat/Hello-World.git#master"

		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD %s /repo-branch
ADD %s /repo-ref
ADD %s /repo-fragment
RUN test -f /repo-branch/README
RUN test -f /repo-ref/README
RUN test -f /repo-fragment/README
CMD ["sh", "-c", "cat /repo-branch/README && echo --- && cat /repo-ref/README && echo --- && cat /repo-fragment/README"]
`, alpineImage, branchURL, refURL, fragmentURL))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Hello World!")
	})

	t.Run("add-http-plain-file", func(ctx context.Context, t *testctx.T) {
		srv := c.Container().
			From(busyboxImage).
			WithWorkdir("/srv").
			WithNewFile("README", "hello-from-http\n").
			WithDefaultArgs([]string{"httpd", "-v", "-f"}).
			WithExposedPort(80).
			AsService().
			WithHostname("fileserver")

		_, err := srv.Start(ctx)
		require.NoError(t, err)

		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD http://fileserver/README /downloads/README
CMD ["cat", "/downloads/README"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello-from-http", strings.TrimSpace(out))
	})

	t.Run("add-local-archive-unpacks", func(ctx context.Context, t *testctx.T) {
		dir := c.Container().
			From(busyboxImage).
			WithWorkdir("/ctx").
			WithExec([]string{"sh", "-c", "mkdir -p inner && echo hello-from-archive > inner/hello.txt && tar cf archive.tar inner"}).
			Directory("/ctx").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD archive.tar /out/
CMD ["cat", "/out/inner/hello.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello-from-archive", strings.TrimSpace(out))
	})

	t.Run("add-non-archive-falls-back-to-plain-copy", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("archive.tar", "not-an-archive\n").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ADD archive.tar /out/plain.txt
CMD ["cat", "/out/plain.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "not-an-archive", strings.TrimSpace(out))
	})

	t.Run("workdir-created-with-named-user-ownership", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN addgroup -g 4321 appgrp && adduser -D -u 1234 -G appgrp app
USER app:appgrp
WORKDIR /work
CMD ["sh", "-lc", "stat -c '%%u:%%g' /work"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1234:4321", strings.TrimSpace(out))
	})

	t.Run("copy-chmod-recursive", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("root.txt", "root").
			WithNewFile("nested/file.txt", "nested").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY --chmod=751 . /app/
`, alpineImage))

		stdout, err := dir.DockerBuild().
			WithExec([]string{"sh", "-lc", "stat -c '%a %n' /app /app/root.txt /app/nested /app/nested/file.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "751 /app")
		require.Contains(t, stdout, "751 /app/root.txt")
		require.Contains(t, stdout, "751 /app/nested")
		require.Contains(t, stdout, "751 /app/nested/file.txt")
	})

	t.Run("copy-chmod-explicit-file-destination", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("input.txt", "explicit-file-dest").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY --chmod=751 input.txt /app/out.txt
`, alpineImage))

		ctr := dir.DockerBuild()
		contents, err := ctr.File("/app/out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "explicit-file-dest", strings.TrimSpace(contents))

		permOut, err := ctr.WithExec([]string{"stat", "-c", "%a", "/app/out.txt"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "751", strings.TrimSpace(permOut))
	})

	t.Run("copy-group-only-chown", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("input.txt", "group-only-chown").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY --chown=:123 input.txt /app/out.txt
`, alpineImage))

		ctr := dir.DockerBuild()
		contents, err := ctr.File("/app/out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "group-only-chown", strings.TrimSpace(contents))

		ownerOut, err := ctr.WithExec([]string{"stat", "-c", "%u:%g", "/app/out.txt"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "0:123", strings.TrimSpace(ownerOut))
	})

	t.Run("copy-named-chown", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("input.txt", "named-chown").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN addgroup -g 4321 agroup && adduser -D -u 1234 -G agroup auser
COPY --chown=auser:agroup input.txt /app/out.txt
`, alpineImage))

		ctr := dir.DockerBuild()
		contents, err := ctr.File("/app/out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "named-chown", strings.TrimSpace(contents))

		ownerOut, err := ctr.WithExec([]string{"stat", "-c", "%u:%g %U:%G", "/app/out.txt"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1234:4321 auser:agroup", strings.TrimSpace(ownerOut))
	})

	t.Run("copy-stage-root-to-subdir", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s AS outfull
RUN mkdir -p /lib/systemd/system && echo ok >/lib/systemd/system/containerd.service

FROM %s
COPY --from=outfull / /usr/local/
`, alpineImage, alpineImage))

		_, err := dir.DockerBuild().
			WithExec([]string{"sh", "-lc", "test -f /usr/local/lib/systemd/system/containerd.service"}).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("copy-multi-stage-service-layout", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s AS build-containerd
RUN mkdir -p /out/amd64 && echo bin >/out/amd64/containerd && echo svc >/out/containerd.service

FROM %s AS build-full
COPY --from=build-containerd /out/amd64/* /out/bin/
COPY --from=build-containerd /out/containerd.service /out/lib/systemd/system/containerd.service

FROM %s AS out-full
COPY --from=build-full /out /

FROM %s
COPY --from=out-full / /usr/local/
`, alpineImage, alpineImage, alpineImage, alpineImage))

		listing, err := dir.DockerBuild().
			WithExec([]string{"sh", "-lc", "find /usr/local -maxdepth 6 -type f | sort"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, listing, "/usr/local/lib/systemd/system/containerd.service")
	})

	t.Run("copy-wildcards", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("wild/a.txt", "A").
			WithNewFile("wild/b.txt", "B").
			WithNewFile("wild/c.md", "C").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY wild/*.txt /out/
RUN test -f /out/a.txt && test -f /out/b.txt && test ! -e /out/c.md
CMD ["sh", "-c", "cat /out/a.txt && cat /out/b.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "AB", strings.TrimSpace(out))
	})

	t.Run("copy-variable-substitution", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("alt.go", "package alt\n").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
ARG SRC=main.go
COPY ${SRC} /tmp/out.go
CMD ["cat", "/tmp/out.go"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "package main")

		opts := dagger.DirectoryDockerBuildOpts{
			BuildArgs: []dagger.BuildArg{
				{Name: "SRC", Value: "alt.go"},
			},
		}
		out, err = dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "package alt\n", out)
	})

	t.Run("copy-through-symlink-context", func(ctx context.Context, t *testctx.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink context behavior is unstable on windows hosts")
		}

		dir := c.Directory().
			WithNewFile("real/file.txt", "symlink-copy-ok\n").
			WithSymlink("real", "linkdir").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY linkdir/file.txt /copied.txt
CMD ["cat", "/copied.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "symlink-copy-ok", strings.TrimSpace(out))
	})

	t.Run("copy-through-symlink-multi-stage", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s AS src
RUN mkdir -p /real && echo symlink-multistage-ok > /real/file.txt && ln -s /real /link
FROM %s
COPY --from=src /link/file.txt /final.txt
CMD ["cat", "/final.txt"]
`, alpineImage, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "symlink-multistage-ok", strings.TrimSpace(out))
	})

	t.Run("copy-relative-with-workdir", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("rel.txt", "relative-copy-ok\n").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
WORKDIR /a/b
COPY rel.txt .
COPY rel.txt rel2.txt
RUN test -f /a/b/rel.txt && test -f /a/b/rel2.txt
CMD ["sh", "-c", "cat /a/b/rel.txt && cat /a/b/rel2.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "relative-copy-ok\nrelative-copy-ok", strings.TrimSpace(out))
	})

	t.Run("copy-destination-dot-and-trailing-slash-semantics", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("src.txt", "dest-semantics-ok\n").
			WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY src.txt /abs/
COPY src.txt /absdot/.
WORKDIR /work
COPY src.txt .
RUN test -f /abs/src.txt && test -f /absdot/src.txt && test -f /work/src.txt
CMD ["sh", "-c", "cat /abs/src.txt && cat /absdot/src.txt && cat /work/src.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "dest-semantics-ok\ndest-semantics-ok\ndest-semantics-ok", strings.TrimSpace(out))
	})

	t.Run("invalid-dockerfile-negative-paths", func(ctx context.Context, t *testctx.T) {
		t.Run("invalid instruction", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", `FRO alpine`)
			_, err := dir.DockerBuild().Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "unknown instruction")
		})

		t.Run("invalid command arity", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
COPY
`, alpineImage))
			_, err := dir.DockerBuild().Sync(ctx)
			require.Error(t, err)
		})

		t.Run("invalid JSON command", func(ctx context.Context, t *testctx.T) {
			t.Skip("TODO: add stable invalid-JSON Dockerfile command case; parser currently accepts attempted malformed forms in this path")
		})
	})

	t.Run("run-mount-cache-basic", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN --mount=type=cache,target=/cache sh -c 'echo cache-ok > /cache/value && cp /cache/value /tmp/first'
RUN --mount=type=cache,target=/cache sh -c 'cp /cache/value /tmp/second'
CMD ["sh", "-c", "cat /tmp/first && cat /tmp/second"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "cache-ok\ncache-ok", strings.TrimSpace(out))
	})

	t.Run("run-mount-tmpfs-basic", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN --mount=type=tmpfs,target=/mnt sh -c 'echo tmpfs-ok > /mnt/value && cp /mnt/value /tmp/out'
CMD ["cat", "/tmp/out"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "tmpfs-ok", strings.TrimSpace(out))
	})

	t.Run("run-mount-bind-readonly", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("mounted.txt", "readonly-bind-data").
			WithNewFile("Dockerfile", fmt.Sprintf(`# syntax=docker/dockerfile:1.7
FROM %s
RUN --mount=type=bind,source=.,target=/mnt,readonly sh -lc 'if touch /mnt/should-fail 2>/dev/null; then echo writable > /mount-mode.txt; else echo readonly > /mount-mode.txt; fi; cat /mnt/mounted.txt > /copied.txt'
CMD ["cat", "/copied.txt"]
`, alpineImage))

		ctr := dir.DockerBuild()
		mode, err := ctr.File("/mount-mode.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "readonly", strings.TrimSpace(mode))

		copied, err := ctr.File("/copied.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "readonly-bind-data", strings.TrimSpace(copied))
	})

	t.Run("run-mount-bind-file", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("pyproject.toml", "[project]\nname = \"bind-file\"\n").
			WithNewFile("Dockerfile", fmt.Sprintf(`# syntax=docker/dockerfile:1.7
FROM %s
RUN --mount=type=bind,source=pyproject.toml,target=pyproject.toml sh -lc 'cat pyproject.toml > /copied.txt'
CMD ["cat", "/copied.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "[project]\nname = \"bind-file\"", strings.TrimSpace(out))
	})

	t.Run("run-mount-bind-file-metadata", func(ctx context.Context, t *testctx.T) {
		dir := c.Container().
			From(alpineImage).
			WithWorkdir("/ctx").
			WithExec([]string{"sh", "-lc", `
set -eu
printf 'exec-file\n' > exec-file
chmod 0755 exec-file
printf 'setuid-file\n' > setuid-file
chmod 4755 setuid-file
printf 'readonly-file\n' > readonly.txt
`}).
			Directory("/ctx").
			WithNewFile("Dockerfile", fmt.Sprintf(`# syntax=docker/dockerfile:1.7
FROM %s
RUN --mount=type=bind,source=exec-file,target=/exec-file \
    --mount=type=bind,source=setuid-file,target=/setuid-file \
    --mount=type=bind,source=readonly.txt,target=/readonly.txt,readonly \
    sh -lc '{ stat -c "%%a %%n" /exec-file /setuid-file; if sh -c "echo mutate > /readonly.txt" 2>/dev/null; then echo writable; else echo readonly; fi; } > /result.txt'
CMD ["cat", "/result.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "755 /exec-file\n4755 /setuid-file\nreadonly", strings.TrimSpace(out))
	})

	t.Run("run-mount-bind-non-sticky", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().
			WithNewFile("ctx.txt", "non-sticky-bind").
			WithNewFile("Dockerfile", fmt.Sprintf(`# syntax=docker/dockerfile:1.7
FROM %s
RUN --mount=type=bind,source=.,target=/mnt,readonly sh -lc 'cat /mnt/ctx.txt > /copied.txt'
RUN test ! -e /mnt/ctx.txt
CMD ["cat", "/copied.txt"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "non-sticky-bind", strings.TrimSpace(out))
	})

	t.Run("run-network-none", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN --network=none sh -c 'echo network-none > /status'
CMD ["cat", "/status"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "network-none", strings.TrimSpace(out))
	})

	t.Run("run-network-host", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", fmt.Sprintf(`FROM %s
RUN --network=host sh -c 'echo network-host > /status'
CMD ["cat", "/status"]
`, alpineImage))

		out, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "network-host", strings.TrimSpace(out))
	})

	t.Run("with build args", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
ARG FOOARG=bar
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=$FOOARG
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		opts := dagger.DirectoryDockerBuildOpts{
			BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}},
		}
		env, err = dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=barbar\n")
	})

	t.Run("with target", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+` AS base
CMD echo "base"

FROM base AS stage1
CMD echo "stage1"

FROM base AS stage2
CMD echo "stage2"
`)
		output, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		opts := dagger.DirectoryDockerBuildOpts{Target: "stage1"}
		output, err = dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})

	t.Run("with heredoc", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
RUN <<EOF
#!/bin/sh
echo "hello from heredoc" > /heredoc-output
EOF
CMD cat /heredoc-output
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "hello from heredoc")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "hello from heredoc")
		})
	})

	t.Run("with build secrets", func(ctx context.Context, t *testctx.T) {
		sec := c.SetSecret("my-secret", "barbar")

		dockerfile := `FROM ` + alpineImage + `
WORKDIR /src
RUN --mount=type=secret,id=my-secret,required=true test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret,required=true cp /run/secrets/my-secret /secret
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		dockerfile = `FROM ` + alpineImage + `
WORKDIR /src
RUN --mount=type=secret,id=my-secret,required=true,env=MY_SECRET sh -c 'test "$MY_SECRET" = "barbar" && printf "%s" "$MY_SECRET" > /env'
CMD sh -c 'cat /env && echo && cat /env | tr "[a-z]" "[A-Z]"'
`

		t.Run("env builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("env remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})
	})

	t.Run("missing secret fails when required is set", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
RUN --mount=type=secret,id=my-secret,required=true echo this should not run
`
		dir := baseDir.WithNewFile("Dockerfile", dockerfile)

		_, err := dir.DockerBuild().Sync(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `secret "my-secret" is required but no secret mappings were provided`)
	})

	t.Run("missing secret is ok when required is false", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
RUN --mount=type=secret,id=my-secret,required=false echo this is fine
`
		dir := baseDir.WithNewFile("Dockerfile", dockerfile)

		_, err := dir.DockerBuild().Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("with unknown build secrets", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
WORKDIR /src
RUN --mount=type=secret,id=my-secret echo "foofoo" > /secret 
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "foofoo")
			require.Contains(t, stdout, "FOOFOO")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "foofoo")
			require.Contains(t, stdout, "FOOFOO")
		})
	})

	t.Run("prevent duplicate secret transform", func(ctx context.Context, t *testctx.T) {
		sec := c.SetSecret("my-secret", "barbar")

		// src is a directory that has a secret dependency in its build graph
		dir := c.Container().
			From(alpineImage).
			WithWorkdir("/src").
			WithMountedSecret("/run/secret", sec).
			WithExec([]string{"cat", "/run/secret"}).
			WithNewFile("Dockerfile", `
			FROM alpine
			COPY / /
			`).
			Directory("/src")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("just build, don't execute", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nCMD false")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NoError(t, err)

		_, err = dir.DockerBuild().WithExec(nil).Sync(ctx)
		require.NotEmpty(t, err)
	})

	t.Run("just build, short-circuit", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN false")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NotEmpty(t, err)
	})

	t.Run("confirm .dockerignore compatibility with docker", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("foo", "foo-contents").
			WithNewFile("bar", "bar-contents").
			WithNewFile("baz", "baz-contents").
			WithNewFile("bay", "bay-contents").
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
	WORKDIR /src
	COPY . .
	`).
			WithNewFile(".dockerignore", `
	ba*
	Dockerfile
	!bay
	.dockerignore
	`)
		content, err := dir.DockerBuild().Directory("/src").File("foo").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo-contents", content)

		cts, err := dir.DockerBuild().Directory("/src").File(".dockerignore").Contents(ctx)
		require.ErrorContains(t, err, ".dockerignore: no such file or directory", fmt.Sprintf("cts is %s", cts))

		_, err = dir.DockerBuild().Directory("/src").File("Dockerfile").Contents(ctx)
		require.ErrorContains(t, err, "Dockerfile: no such file or directory")

		_, err = dir.DockerBuild().Directory("/src").File("bar").Contents(ctx)
		require.ErrorContains(t, err, "bar: no such file or directory")

		_, err = dir.DockerBuild().Directory("/src").File("baz").Contents(ctx)
		require.ErrorContains(t, err, "baz: no such file or directory")

		content, err = dir.DockerBuild().Directory("/src").File("bay").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bay-contents", content)
	})

	t.Run("onbuild command is published", func(ctx context.Context, t *testctx.T) {
		testRef := registryRef("dockerfile-publish")

		pushedRef, err := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
	ONBUILD COPY some-file-that-might-exist .
	`).DockerBuild().Publish(ctx, testRef)

		// The initial build doesn't depend on some-file-that-might-exist existing
		require.NoError(t, err)
		require.Contains(t, pushedRef, "@sha256:")

		// However, when we perform a second build that references the above Dockerfile
		// it should return an error since some-file-that-might-exist doesn't actually exist
		_, err = baseDir.
			WithNewFile("Dockerfile",
				`FROM `+pushedRef+`
	`).DockerBuild().Sync(ctx)
		require.ErrorContains(t, err, "lstat /some-file-that-might-exist: no such file or directory")

		// Test again, after some-file-that-might-exist is created.
		s, err := baseDir.
			WithNewFile("some-file-that-might-exist", "existence is futile").
			WithNewFile("Dockerfile",
				`FROM `+pushedRef+`
	`).DockerBuild().File("some-file-that-might-exist").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "existence is futile", s)
	})

	t.Run("healthcheck", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
HEALTHCHECK --interval=21s --timeout=4s --start-period=9s --start-interval=2s --retries=5 CMD ["sh","-c","test -d /"]
`
		dir := baseDir.WithNewFile("Dockerfile", dockerfile)

		healthcheck := dir.DockerBuild().DockerHealthcheck()

		interval, err := healthcheck.Interval(ctx)
		require.NoError(t, err)
		require.Equal(t, "21s", interval)

		timeout, err := healthcheck.Timeout(ctx)
		require.NoError(t, err)
		require.Equal(t, "4s", timeout)

		startPeriod, err := healthcheck.StartPeriod(ctx)
		require.NoError(t, err)
		require.Equal(t, "9s", startPeriod)

		startInterval, err := healthcheck.StartInterval(ctx)
		require.NoError(t, err)
		require.Equal(t, "2s", startInterval)

		retries, err := healthcheck.Retries(ctx)
		require.NoError(t, err)
		require.Equal(t, 5, retries)

		args, err := healthcheck.Args(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sh", "-c", "test -d /"}, args)
	})
}

// TestDockerBuildBuildPlatformArg is a regression repro for the layer-caching regression
// reported upgrading 0.20.8 -> 0.21.x for Dockerfiles that pin stages to the build platform
// via `FROM --platform=$BUILDPLATFORM`.
//
// The predefined BUILDPLATFORM build arg must resolve to the engine's *native* (build host)
// platform, independent of the target platform being built. That is what BuildKit's frontend
// did (and what 0.20.8 relied on): dockerui.Client.init always sets BuildPlatforms to the
// worker's native platform (see internal/buildkit/frontend/dockerui/config.go).
//
// In 0.21.x the dagql-native dockerBuild path (core/container.go Container.Build) constructs
// dockerfile2llb.ConvertOpt with TargetPlatform set but WITHOUT setting BuildPlatforms, so
// buildPlatformOpt (internal/buildkit/frontend/dockerfile/dockerfile2llb/platform.go) falls
// back to using TargetPlatform as the build platform. That makes BUILDPLATFORM ==
// TARGETPLATFORM, which both emulates stages that should run natively on the host and diverges
// the cache key per target platform, so the identical $BUILDPLATFORM stages of an amd64 vs
// arm64 build stop sharing cache.
func (DockerfileSuite) TestDockerBuildBuildPlatformArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	nativePlatform, err := c.DefaultPlatform(ctx)
	require.NoError(t, err)
	nativeUname, ok := platformToUname[nativePlatform]
	require.True(t, ok, "unsupported native platform %q", nativePlatform)

	// a target platform that differs from the engine's native platform
	targetPlatform := dagger.Platform("linux/arm64")
	if nativePlatform == targetPlatform {
		targetPlatform = "linux/amd64"
	}
	require.NotEqual(t, string(nativePlatform), string(targetPlatform))

	dockerfile := "FROM --platform=$BUILDPLATFORM " + busyboxImage + `
ARG BUILDPLATFORM
ARG TARGETPLATFORM
RUN printf '%s' "$BUILDPLATFORM" > /buildplatform.txt
RUN printf '%s' "$TARGETPLATFORM" > /targetplatform.txt
RUN uname -m > /uname.txt
`
	dir := c.Directory().WithNewFile("Dockerfile", dockerfile)

	buildInfo := func(ctx context.Context, t *testctx.T, opts dagger.DirectoryDockerBuildOpts) (buildPlatform, targetPlatformArg, uname string) {
		ctr := dir.DockerBuild(opts)
		buildPlatform, err := ctr.File("/buildplatform.txt").Contents(ctx)
		require.NoError(t, err)
		targetPlatformArg, err = ctr.File("/targetplatform.txt").Contents(ctx)
		require.NoError(t, err)
		unameOut, err := ctr.File("/uname.txt").Contents(ctx)
		require.NoError(t, err)
		return buildPlatform, targetPlatformArg, strings.TrimSpace(unameOut)
	}

	t.Run("BUILDPLATFORM resolves to the native build platform, not the target", func(ctx context.Context, t *testctx.T) {
		buildPlatform, targetPlatformArg, uname := buildInfo(ctx, t, dagger.DirectoryDockerBuildOpts{
			Platform: targetPlatform,
		})

		// sanity: TARGETPLATFORM must reflect the requested target
		require.Equal(t, string(targetPlatform), targetPlatformArg)

		// BUILDPLATFORM must be the engine's native/build-host platform, NOT the target.
		require.Equal(t, string(nativePlatform), buildPlatform,
			"BUILDPLATFORM should be the native build platform, got the target platform instead")

		// the $BUILDPLATFORM-pinned stage must run natively (no emulation), so uname reports
		// the native architecture rather than the (emulated) target architecture.
		require.Equal(t, nativeUname, uname,
			"$BUILDPLATFORM stage should build for the native arch, not be emulated to the target")
	})

	t.Run("$BUILDPLATFORM stages are identical across target platforms", func(ctx context.Context, t *testctx.T) {
		// The whole point of --platform=$BUILDPLATFORM is that these stages are identical
		// regardless of which target arch we ultimately build for, so they share cache. If the
		// build platform tracks the target, the two builds diverge into separate DAGs.
		_, _, unameNativeTarget := buildInfo(ctx, t, dagger.DirectoryDockerBuildOpts{
			Platform: nativePlatform,
		})
		_, _, unameCrossTarget := buildInfo(ctx, t, dagger.DirectoryDockerBuildOpts{
			Platform: targetPlatform,
		})
		require.Equal(t, unameNativeTarget, unameCrossTarget,
			"$BUILDPLATFORM stage differs across target platforms; the DAGs/cache keys have diverged")
	})

	t.Run("explicit BUILDPLATFORM build arg restores native behavior", func(ctx context.Context, t *testctx.T) {
		// The reported workaround: pin BUILDPLATFORM to the native platform explicitly.
		buildPlatform, _, uname := buildInfo(ctx, t, dagger.DirectoryDockerBuildOpts{
			Platform: targetPlatform,
			BuildArgs: []dagger.BuildArg{
				{Name: "BUILDPLATFORM", Value: string(nativePlatform)},
			},
		})
		require.Equal(t, string(nativePlatform), buildPlatform)
		require.Equal(t, nativeUname, uname)
	})
}

func (DockerfileSuite) TestBuildMergesWithParent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a container with envs variables and labels
	testCtr := c.Directory().WithNewFile("Dockerfile",
		`FROM `+alpineImage+`
ENV FOO=BAR
LABEL "com.example.test"="foo"
EXPOSE 8080
`,
	).DockerBuild()

	env, err := testCtr.EnvVariable(ctx, "FOO")
	require.NoError(t, err)
	require.Equal(t, "BAR", env)

	labelShouldExist, err := testCtr.Label(ctx, "com.example.test")
	require.NoError(t, err)
	require.Equal(t, "foo", labelShouldExist)

	// FIXME: Pretty clunky to work with lists of objects from the SDK
	// so test the exposed ports with a query string for now.
	cid, err := testCtr.ID(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			ExposedPorts []core.Port
		} `json:"container"`
	}](c, t, `
        query Test($id: ID!) {
            container: node(id: $id) {
                ... on Container {
                    exposedPorts {
                        port
                        protocol
                        description
                    }
                }
            }
        }`,
		&testutil.QueryOptions{
			Variables: map[string]any{
				"id": cid,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 1)

	// random order since ImageConfig.ExposedPorts is a map
	for _, p := range res.Container.ExposedPorts {
		require.Equalf(t, core.NetworkProtocolTCP, p.Protocol, "unexpected protocol for port %d", p.Port)
		switch p.Port {
		case 8080:
			require.Nil(t, p.Description)
		default:
			t.Fatalf("unexpected port %d", p.Port)
		}
	}
}

func (DockerfileSuite) TestDockerBuildSSH(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a local unix socket echo server
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(conn, conn)
			if err != nil {
				t.Logf("copy: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = conn.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	sockID, err := c.Host().UnixSocket(sock).ID(ctx)
	require.NoError(t, err)

	dockerfile := `FROM ` + alpineImage + `
RUN apk add netcat-openbsd
RUN --mount=type=ssh sh -c 'echo -n hello | nc -w1 -N -U $SSH_AUTH_SOCK > /result'
`

	t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", dockerfile)
		dirID, err := dir.ID(ctx)
		require.NoError(t, err)

		res, err := testutil.QueryWithClient[struct {
			Directory struct {
				DockerBuild struct {
					File struct {
						Contents string
					}
				}
			} `json:"directory"`
		}](c, t, `query Test($dir: ID!, $sock: ID!) {
			directory: node(id: $dir) {
				... on Directory {
					dockerBuild(ssh: $sock) {
						file(path: "/result") {
							contents
						}
					}
				}
			}
		}`, &testutil.QueryOptions{
			Variables: map[string]any{
				"dir":  dirID,
				"sock": sockID,
			},
		})
		require.NoError(t, err)
		require.Equal(t, "hello", res.Directory.DockerBuild.File.Contents)
	})

	t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
		dirID, err := dir.ID(ctx)
		require.NoError(t, err)

		res, err := testutil.QueryWithClient[struct {
			Directory struct {
				DockerBuild struct {
					File struct {
						Contents string
					}
				}
			} `json:"directory"`
		}](c, t, `query Test($dir: ID!, $sock: ID!) {
			directory: node(id: $dir) {
				... on Directory {
					dockerBuild(ssh: $sock) {
						file(path: "/result") {
							contents
						}
					}
				}
			}
		}`, &testutil.QueryOptions{
			Variables: map[string]any{
				"dir":  dirID,
				"sock": sockID,
			},
		})
		require.NoError(t, err)
		require.Equal(t, "hello", res.Directory.DockerBuild.File.Contents)
	})

	t.Run("without ssh socket fails", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", dockerfile)
		_, err := dir.DockerBuild().Sync(ctx)
		require.Error(t, err)
	})
}

func (DockerfileSuite) TestAddHTTPDoesNotUnpack(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := c.Container().
		From(busyboxImage).
		WithWorkdir("/srv").
		WithExec([]string{"sh", "-c", "mkdir mydir && echo remotedata > mydir/data && tar czf remotedir.tar.gz mydir"}).
		WithDefaultArgs([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("fileserver")

	_, err := srv.Start(ctx)
	require.NoError(t, err)

	dir := c.Container().
		From(alpineImage).
		Directory(".").
		WithNewFile("Dockerfile",
			`FROM `+golangImage+`
WORKDIR /work
ADD http://fileserver/remotedir.tar.gz this-should-not-unpack
`)

	ctr, err := dir.DockerBuild().Sync(ctx)
	require.NoError(t, err)

	_, err = ctr.WithExec([]string{"test", "-f", "this-should-not-unpack"}).Sync(ctx)
	require.NoError(t, err)

	s, err := ctr.WithExec([]string{"sh", "-c", "mkdir the-dir && tar xzf this-should-not-unpack -C the-dir"}).File("the-dir/mydir/data").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "remotedata\n", s)
}

func (DockerfileSuite) TestCopyExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := c.Directory().
		WithNewDirectory("data").
		WithNewFile("data/yes", "oui").
		WithNewFile("data/no", "nein")

	baseDir := contextDir

	dir := baseDir.
		WithNewFile("Dockerfile",
			`# syntax=docker/dockerfile:1
                       FROM `+alpineImage+`
COPY --exclude=no data data
`)
	found, err := dir.DockerBuild().Exists(ctx, "data/yes")
	require.NoError(t, err)
	require.True(t, found)

	found, err = dir.DockerBuild().Exists(ctx, "data/no")
	require.NoError(t, err)
	require.False(t, found)
}

func (DockerfileSuite) TestAddUnpack(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := c.Container().
		From(alpineImage).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", "mkdir -p payload && printf hello > payload/hello.txt && tar -cf archive.tar payload"}).
		Directory(".")

	t.Run("local archive can disable unpacking", func(ctx context.Context, t *testctx.T) {
		dir := contextDir.
			WithNewFile("Dockerfile", `FROM `+alpineImage+`
ADD --unpack=false archive.tar /out/
`)
		ctr := dir.DockerBuild()

		found, err := ctr.Exists(ctx, "/out/archive.tar")
		require.NoError(t, err)
		require.True(t, found)

		found, err = ctr.Exists(ctx, "/out/payload/hello.txt")
		require.NoError(t, err)
		require.False(t, found)
	})
}

// TestDockerBuildContextChangeLayerCache verifies that COPY/ADD steps (and
// anything downstream of them) keep their layer cache when files in the build
// context that the COPY does not select are modified.
func (DockerfileSuite) TestDockerBuildContextChangeLayerCache(ctx context.Context, t *testctx.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app", "main.txt"), []byte("v1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(`FROM `+alpineImage+`
COPY app /app
RUN head -c16 /dev/urandom | sha256sum > /copy-stamp
RUN --mount=type=bind,source=app/main.txt,target=/mnt/main.txt head -c16 /dev/urandom | sha256sum > /bind-stamp
`), 0o644))

	// Use a fresh client per build: host.directory is cached per session, and
	// the scenario being modeled is successive pipeline runs (one session
	// each) against the same engine.
	stamps := func() (string, string) {
		c := connect(ctx, t)
		defer c.Close()
		ctr := c.Host().Directory(dir).DockerBuild()
		copyStamp, err := ctr.File("/copy-stamp").Contents(ctx)
		require.NoError(t, err)
		bindStamp, err := ctr.File("/bind-stamp").Contents(ctx)
		require.NoError(t, err)
		return copyStamp, bindStamp
	}

	copyStamp1, bindStamp1 := stamps()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("two"), 0o644))
	copyStamp2, bindStamp2 := stamps()
	require.Equal(t, copyStamp1, copyStamp2, "unrelated context change invalidated COPY layer cache")
	require.Equal(t, bindStamp1, bindStamp2, "unrelated context change invalidated bind-mount RUN layer cache")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app", "main.txt"), []byte("v2"), 0o644))
	copyStamp3, _ := stamps()
	require.NotEqual(t, copyStamp1, copyStamp3, "COPY'd content change did not invalidate downstream RUN")
}

// TestDockerBuildEmptyWildcardCopy verifies that a COPY with a wildcard that
// matches nothing (and a destination that never gets created) still builds:
// content hashing the copy result must not fail on the absent destination.
func (DockerfileSuite) TestDockerBuildEmptyWildcardCopy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(`FROM `+alpineImage+`
COPY optional-* /out/
COPY present.txt /present.txt
`), 0o644))

	ctr := c.Host().Directory(dir).DockerBuild()
	contents, err := ctr.File("/present.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi", contents)

	exists, err := ctr.Exists(ctx, "/out")
	require.NoError(t, err)
	require.False(t, exists, "unmatched wildcard COPY should not create the destination")
}
