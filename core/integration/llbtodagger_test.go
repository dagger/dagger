package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/util/llbtodagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type LLBToDaggerSuite struct{}

func TestLLBToDagger(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LLBToDaggerSuite{})
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDSimple(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"msg.txt": "hello-from-llb",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
WORKDIR /work
COPY msg.txt /work/
RUN test -s /work/msg.txt
CMD ["cat", "/work/msg.txt"]
`)

	_, err := ctr.Sync(ctx)
	require.NoError(t, err)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello-from-llb", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyChmod(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"root.txt":        "root",
		"nested/file.txt": "nested",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
COPY --chmod=751 . /app/
`)

	stdout, err := ctr.WithExec([]string{"sh", "-lc", "stat -c '%a %n' /app /app/root.txt /app/nested /app/nested/file.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "751 /app")
	require.Contains(t, stdout, "751 /app/root.txt")
	require.Contains(t, stdout, "751 /app/nested")
	require.Contains(t, stdout, "751 /app/nested/file.txt")
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyChmodExplicitFileDest(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"input.txt": "explicit-file-dest",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
COPY --chmod=751 input.txt /app/out.txt
`)

	contents, err := ctr.File("/app/out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "explicit-file-dest", strings.TrimSpace(contents))

	permOut, err := ctr.WithExec([]string{"stat", "-c", "%a", "/app/out.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "751", strings.TrimSpace(permOut))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyGroupOnlyChown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"input.txt": "group-only-chown",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
COPY --chown=:123 input.txt /app/out.txt
`)

	contents, err := ctr.File("/app/out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "group-only-chown", strings.TrimSpace(contents))

	ownerOut, err := ctr.WithExec([]string{"stat", "-c", "%u:%g", "/app/out.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "0:123", strings.TrimSpace(ownerOut))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyNamedChown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"input.txt": "named-chown",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
RUN addgroup -g 4321 agroup && adduser -D -u 1234 -G agroup auser
COPY --chown=auser:agroup input.txt /app/out.txt
`)

	contents, err := ctr.File("/app/out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "named-chown", strings.TrimSpace(contents))

	ownerOut, err := ctr.WithExec([]string{"stat", "-c", "%u:%g %U:%G", "/app/out.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "1234:4321 auser:agroup", strings.TrimSpace(ownerOut))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDAddHTTP(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
ADD https://raw.githubusercontent.com/octocat/Hello-World/master/README /downloads/README
RUN test -s /downloads/README
CMD ["sh", "-c", "test -s /downloads/README && echo ok"]
`)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDAddGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
ADD https://github.com/octocat/Hello-World.git#master /repo
RUN test -f /repo/README
CMD ["sh", "-c", "test -s /repo/README && echo ok"]
`)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDComplex(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"input.txt":  "llbtodagger-e2e",
		"second.txt": "secondary",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+` AS base
WORKDIR /work
COPY --chmod=751 input.txt /work/
COPY --chmod=640 second.txt /work/
ADD https://raw.githubusercontent.com/octocat/Hello-World/master/README /work/vendor/http-readme
ADD https://github.com/octocat/Hello-World.git#master /work/vendor/hello
RUN cat /work/input.txt > /work/out.txt && cat /work/second.txt > /work/out-second.txt && test -s /work/vendor/http-readme && test -f /work/vendor/hello/README

FROM `+alpineImage+`
WORKDIR /final
RUN addgroup -g 4321 agroup && adduser -D -u 1234 -G agroup auser
COPY --chmod=751 --from=base /work/out.txt /final/
COPY --chmod=640 --from=base /work/out-second.txt /final/
COPY --chown=auser:agroup --from=base /work/out-second.txt /final/out-named.txt
ENV RESULT=success
USER root
LABEL com.example.suite=llbtodagger
EXPOSE 8090
ENTRYPOINT ["cat"]
CMD ["/final/out.txt"]
`)

	contents, err := ctr.File("/final/out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "llbtodagger-e2e", strings.TrimSpace(contents))

	out, err := ctr.WithExec([]string{"cat", "/final/out.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "llbtodagger-e2e", strings.TrimSpace(out))

	secondOut, err := ctr.WithExec([]string{"cat", "/final/out-second.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "secondary", strings.TrimSpace(secondOut))

	permOut, err := ctr.WithExec([]string{"stat", "-c", "%a", "/final/out.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "751", strings.TrimSpace(permOut))

	secondPermOut, err := ctr.WithExec([]string{"stat", "-c", "%a", "/final/out-second.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "640", strings.TrimSpace(secondPermOut))

	namedOwnerOut, err := ctr.WithExec([]string{"stat", "-c", "%u:%g %U:%G", "/final/out-named.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "1234:4321 auser:agroup", strings.TrimSpace(namedOwnerOut))

	workdir, err := ctr.Workdir(ctx)
	require.NoError(t, err)
	require.Equal(t, "/final", workdir)

	user, err := ctr.User(ctx)
	require.NoError(t, err)
	require.Equal(t, "root", user)

	env, err := ctr.EnvVariable(ctx, "RESULT")
	require.NoError(t, err)
	require.Equal(t, "success", env)

	label, err := ctr.Label(ctx, "com.example.suite")
	require.NoError(t, err)
	require.Equal(t, "llbtodagger", label)

	entrypoint, err := ctr.Entrypoint(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"cat"}, entrypoint)

	defaultArgs, err := ctr.DefaultArgs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/final/out.txt"}, defaultArgs)

	ports, err := ctr.ExposedPorts(ctx)
	require.NoError(t, err)
	portSet := map[string]bool{}
	for _, p := range ports {
		n, err := p.Port(ctx)
		require.NoError(t, err)
		proto, err := p.Protocol(ctx)
		require.NoError(t, err)
		portSet[fmt.Sprintf("%d/%s", n, proto)] = true
	}
	require.True(t, portSet["8090/TCP"])
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunMountReadonly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"mounted.txt": "readonly-bind-data",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
# syntax=docker/dockerfile:1.7
FROM `+alpineImage+`
RUN --mount=type=bind,source=.,target=/mnt,readonly sh -lc 'if touch /mnt/should-fail 2>/dev/null; then echo writable > /mount-mode.txt; else echo readonly > /mount-mode.txt; fi; cat /mnt/mounted.txt > /copied.txt'
CMD ["cat", "/copied.txt"]
`)

	mode, err := ctr.File("/mount-mode.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "readonly", strings.TrimSpace(mode))

	copied, err := ctr.File("/copied.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "readonly-bind-data", strings.TrimSpace(copied))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunMountNonSticky(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"ctx.txt": "non-sticky-bind",
	})

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
# syntax=docker/dockerfile:1.7
FROM `+alpineImage+`
RUN --mount=type=bind,source=.,target=/mnt,readonly sh -lc 'cat /mnt/ctx.txt > /copied.txt'
RUN test ! -e /mnt/ctx.txt
CMD ["cat", "/copied.txt"]
`)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "non-sticky-bind", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestConversionDeterministicEncoding(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, map[string]string{
		"msg.txt": "deterministic",
	})

	dockerfile := `
FROM ` + alpineImage + `
WORKDIR /work
COPY msg.txt /work/
RUN cat /work/msg.txt > /work/out.txt
CMD ["cat", "/work/out.txt"]
`

	idA, encA := convertDockerfileToContainerID(ctx, t, c, contextDir, dockerfile)
	idB, encB := convertDockerfileToContainerID(ctx, t, c, contextDir, dockerfile)

	require.Equal(t, idA, idB)
	require.Equal(t, encA, encB)
}

func writeDockerContext(t *testctx.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	if len(files) == 0 {
		return dir
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, rel := range paths {
		abs := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(files[rel]), 0o600))
	}
	return dir
}

func convertDockerfileToLoadedContainer(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	contextDir string,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (*dagger.Container, dagger.ContainerID, string) {
	t.Helper()

	containerID, encoded := convertDockerfileToContainerID(ctx, t, c, contextDir, dockerfile, optFns...)
	return c.LoadContainerFromID(containerID), containerID, encoded
}

func convertDockerfileToContainerID(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	contextDir string,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (dagger.ContainerID, string) {
	t.Helper()

	def, img := dockerfileToDefinitionAndImage(ctx, t, contextDir, dockerfile, optFns...)
	id, err := llbtodagger.DefinitionToID(def, img)
	require.NoError(t, err)

	encoded, err := id.Encode()
	require.NoError(t, err)

	containerID := dagger.ContainerID(encoded)
	// Validate that the ID can actually be loaded.
	_, err = c.LoadContainerFromID(containerID).Sync(ctx)
	require.NoError(t, err)
	return containerID, encoded
}

func dockerfileToDefinitionAndImage(
	ctx context.Context,
	t *testctx.T,
	contextDir string,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (*pb.Definition, *dockerspec.DockerOCIImage) {
	t.Helper()

	mainContext := llb.Local("context", llb.SharedKeyHint(contextDir))
	opt := dockerfile2llb.ConvertOpt{
		MainContext: &mainContext,
		TargetPlatform: &ocispecs.Platform{
			OS:           "linux",
			Architecture: runtime.GOARCH,
		},
	}
	for _, fn := range optFns {
		fn(&opt)
	}

	st, img, _, _, err := dockerfile2llb.Dockerfile2LLB(ctx, []byte(strings.TrimSpace(dockerfile)), opt)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.NotNil(t, img)

	def, err := st.Marshal(ctx)
	require.NoError(t, err)
	return def.ToPB(), img
}
