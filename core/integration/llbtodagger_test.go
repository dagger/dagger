package core

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/config"
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDWorkdirNamedUserOwnership(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
RUN addgroup -g 4321 appgrp && adduser -D -u 1234 -G appgrp app
USER app:appgrp
WORKDIR /work
CMD ["sh", "-lc", "stat -c '%u:%g' /work"]
`)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "1234:4321", strings.TrimSpace(out))
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyDoNotCreateDestPathParentExists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcState := llb.Scratch().File(
		llb.Mkfile("/src.txt", 0o644, []byte("create-dest-path-false")),
	)
	state := llb.Image(alpineImage).
		File(llb.Mkdir("/existing", 0o755, llb.WithParents(true))).
		File(llb.Copy(srcState, "/src.txt", "/existing/out.txt", &llb.CopyInfo{CreateDestPath: false}))

	def := llbStateToDefinition(ctx, t, state)
	id, err := llbtodagger.DefinitionToID(def, nil)
	require.NoError(t, err)

	encoded, err := id.Encode()
	require.NoError(t, err)

	ctr := c.LoadContainerFromID(dagger.ContainerID(encoded))
	_, err = ctr.Sync(ctx)
	require.NoError(t, err)

	contents, err := ctr.File("/existing/out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "create-dest-path-false", strings.TrimSpace(contents))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDCopyDoNotCreateDestPathMissingParent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcState := llb.Scratch().File(
		llb.Mkfile("/src.txt", 0o644, []byte("create-dest-path-false")),
	)
	state := llb.Image(alpineImage).
		File(llb.Copy(srcState, "/src.txt", "/missing/out.txt", &llb.CopyInfo{CreateDestPath: false}))

	def := llbStateToDefinition(ctx, t, state)
	id, err := llbtodagger.DefinitionToID(def, nil)
	require.NoError(t, err)

	encoded, err := id.Encode()
	require.NoError(t, err)

	_, err = c.LoadContainerFromID(dagger.ContainerID(encoded)).Sync(ctx)
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "no such file or directory")
	require.Contains(t, err.Error(), "/missing")
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDAddLocalArchive(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)
	writeTarArchiveWithSingleFile(t, filepath.Join(contextDir, "archive.tar"), "inner/hello.txt", "hello-from-archive")

	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
ADD archive.tar /out/
CMD ["cat", "/out/inner/hello.txt"]
`)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello-from-archive", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDAttemptUnpackNonArchiveFallback(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcState := llb.Scratch().File(
		llb.Mkfile("/plain.txt", 0o644, []byte("plain-copy-fallback")),
	)
	state := llb.Image(alpineImage).File(
		llb.Copy(srcState, "/plain.txt", "/out/plain.txt", &llb.CopyInfo{
			CreateDestPath: true,
			AttemptUnpack:  true,
		}),
	)

	def := llbStateToDefinition(ctx, t, state)
	id, err := llbtodagger.DefinitionToID(def, nil)
	require.NoError(t, err)

	encoded, err := id.Encode()
	require.NoError(t, err)
	ctr := c.LoadContainerFromID(dagger.ContainerID(encoded))

	contents, err := ctr.File("/out/plain.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "plain-copy-fallback", strings.TrimSpace(contents))
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDMetadataExportConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	dockerfile := `
FROM ` + alpineImage + `
RUN echo hi > /out.txt
HEALTHCHECK --interval=21s --timeout=4s --start-period=9s --start-interval=2s --retries=5 CMD ["sh","-c","test -f /out.txt"]
ONBUILD RUN echo child-build
SHELL ["/bin/ash","-eo","pipefail","-c"]
VOLUME ["/cache","/data"]
STOPSIGNAL SIGQUIT
CMD ["cat", "/out.txt"]
`

	_, expectedImage := dockerfileToDefinitionAndImage(ctx, t, contextDir, dockerfile)
	ctr, _, _ := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, dockerfile)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi", strings.TrimSpace(out))

	imagePath := filepath.Join(t.TempDir(), "llbtodagger-metadata.tar")
	actualPath, err := ctr.Export(ctx, imagePath)
	require.NoError(t, err)
	require.Equal(t, imagePath, actualPath)

	dockerManifestBytes := readTarFile(t, imagePath, "manifest.json")
	var dockerManifest []struct {
		Config string
	}
	require.NoError(t, json.Unmarshal(dockerManifestBytes, &dockerManifest))
	require.Len(t, dockerManifest, 1)

	configBytes := readTarFile(t, imagePath, dockerManifest[0].Config)
	var gotImage dockerspec.DockerOCIImage
	require.NoError(t, json.Unmarshal(configBytes, &gotImage))

	require.Equal(t, expectedImage.Config.Healthcheck, gotImage.Config.Healthcheck)
	require.Equal(t, expectedImage.Config.OnBuild, gotImage.Config.OnBuild)
	require.Equal(t, expectedImage.Config.Shell, gotImage.Config.Shell)
	require.Equal(t, expectedImage.Config.Volumes, gotImage.Config.Volumes)
	require.Equal(t, expectedImage.Config.StopSignal, gotImage.Config.StopSignal)
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunNetworkNone(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, encoded := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
RUN --network=none sh -c 'echo network-none > /status'
CMD ["cat", "/status"]
`)

	id := decodeCallID(t, encoded)
	withExec := findFieldInCallChain(id, "withExec")
	require.NotNil(t, withExec)
	noNetwork := withExec.Arg("noNetwork")
	require.NotNil(t, noNetwork)
	require.Equal(t, true, noNetwork.Value().ToInput())

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "network-none", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunNetworkHost(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, encoded := convertDockerfileToLoadedContainer(ctx, t, c, contextDir, `
FROM `+alpineImage+`
RUN --network=host sh -c 'echo network-host > /status'
CMD ["cat", "/status"]
`)

	id := decodeCallID(t, encoded)
	withExec := findFieldInCallChain(id, "withExec")
	require.NotNil(t, withExec)
	hostNetwork := withExec.Arg("hostNetwork")
	require.NotNil(t, hostNetwork)
	require.Equal(t, true, hostNetwork.Value().ToInput())

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "network-host", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunNetworkHostDeniedWhenInsecureRootCapabilitiesDisabled(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	f := false
	engine := devEngineContainer(c, engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
		cfg.Security = &config.Security{
			InsecureRootCapabilities: &f,
		}
		return cfg
	}))
	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(engine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = engineSvc.Stop(ctx)
	})

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(io.Discard))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	contextDir := writeDockerContext(t, nil)
	def, img := dockerfileToDefinitionAndImage(ctx, t, contextDir, `
FROM `+alpineImage+`
RUN --network=host sh -c 'echo denied > /status'
CMD ["cat", "/status"]
`)

	id, err := llbtodagger.DefinitionToIDWithOptions(def, img, llbtodagger.DefinitionToIDOptions{})
	require.NoError(t, err)

	encoded, err := id.Encode()
	require.NoError(t, err)

	_, err = c2.LoadContainerFromID(dagger.ContainerID(encoded)).Sync(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "network.host is not allowed")
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunNoInitOption(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := writeDockerContext(t, nil)

	ctr, _, encoded := convertDockerfileToLoadedContainerWithOptions(
		ctx,
		t,
		c,
		contextDir,
		`
FROM `+alpineImage+`
RUN sh -c 'ps -o pid,comm > /status'
CMD ["cat", "/status"]
`,
		llbtodagger.DefinitionToIDOptions{
			NoInit: true,
		},
	)

	id := decodeCallID(t, encoded)
	withExec := findFieldInCallChain(id, "withExec")
	require.NotNil(t, withExec)
	noInit := withExec.Arg("noInit")
	require.NotNil(t, noInit)
	require.Equal(t, true, noInit.Value().ToInput())

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "1 ps")
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

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunSecretMountAndEnv(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	sec := c.SetSecret("my-secret", "barbar")
	secSDKID, err := sec.ID(ctx)
	require.NoError(t, err)
	var secCallID call.ID
	require.NoError(t, secCallID.Decode(string(secSDKID)))

	contextDir := writeDockerContext(t, nil)

	ctr, _, _ := convertDockerfileToLoadedContainerWithOptions(
		ctx,
		t,
		c,
		contextDir,
		`
FROM `+alpineImage+`
RUN --mount=type=secret,id=my-secret,required=true cp /run/secrets/my-secret /mounted
RUN --mount=type=secret,id=my-secret,required=true,env=MY_SECRET sh -c 'test "$MY_SECRET" = "barbar" && printf "%s" "$MY_SECRET" > /env'
RUN test ! -e /run/secrets/my-secret
CMD ["sh", "-c", "cat /mounted && echo && cat /env"]
`,
		llbtodagger.DefinitionToIDOptions{
			SecretIDsByLLBID: map[string]*call.ID{
				"my-secret": &secCallID,
			},
		},
	)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "barbar\nbarbar", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDRunSSHMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "ssh-agent.sock")

	listener, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					panic(err)
				}
				return
			}
			_, err = io.Copy(conn, conn)
			if err != nil {
				panic(err)
			}
			_ = conn.Close()
		}
	}()

	sock := c.Host().UnixSocket(sockPath)
	sockSDKID, err := sock.ID(ctx)
	require.NoError(t, err)
	var sockCallID call.ID
	require.NoError(t, sockCallID.Decode(string(sockSDKID)))

	contextDir := writeDockerContext(t, nil)
	ctr, _, _ := convertDockerfileToLoadedContainerWithOptions(
		ctx,
		t,
		c,
		contextDir,
		`
FROM `+alpineImage+`
RUN apk add --no-cache netcat-openbsd
RUN --mount=type=ssh,id=my-ssh,required=true,target=/tmp/ssh-agent.sock sh -c 'echo -n hello | nc -w1 -N -U /tmp/ssh-agent.sock > /result'
RUN test ! -S /tmp/ssh-agent.sock
CMD ["cat", "/result"]
`,
		llbtodagger.DefinitionToIDOptions{
			SSHSocketIDsByLLBID: map[string]*call.ID{
				"my-ssh": &sockCallID,
			},
		},
	)

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (LLBToDaggerSuite) TestLoadContainerFromConvertedIDLocalFollowPaths(ctx context.Context, t *testctx.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink followPaths coverage is unstable on windows hosts")
	}

	c := connect(ctx, t)

	contextDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(contextDir, "data"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "data", "target.txt"), []byte("followpaths-ok"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join("data", "target.txt"), filepath.Join(contextDir, "link.txt")))

	st := llb.Local(
		"ctx",
		llb.SharedKeyHint(contextDir),
		llb.IncludePatterns([]string{"link.txt"}),
		llb.FollowPaths([]string{"link.txt"}),
	)
	def := llbStateToDefinition(ctx, t, st)

	id, err := llbtodagger.DefinitionToID(def, nil)
	require.NoError(t, err)
	encoded, err := id.Encode()
	require.NoError(t, err)

	ctr := c.LoadContainerFromID(dagger.ContainerID(encoded))

	linkContents, err := ctr.File("/link.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "followpaths-ok", strings.TrimSpace(linkContents))

	targetContents, err := ctr.File("/data/target.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "followpaths-ok", strings.TrimSpace(targetContents))
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

func writeTarArchiveWithSingleFile(t *testctx.T, archivePath string, filePath string, fileContents string) {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	body := []byte(fileContents)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: filePath,
		Mode: 0o644,
		Size: int64(len(body)),
	}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	require.NoError(t, os.WriteFile(archivePath, buf.Bytes(), 0o600))
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

	containerID, encoded := convertDockerfileToContainerIDWithOptions(
		ctx,
		t,
		c,
		contextDir,
		dockerfile,
		llbtodagger.DefinitionToIDOptions{},
		optFns...,
	)
	return c.LoadContainerFromID(containerID), containerID, encoded
}

func convertDockerfileToLoadedContainerWithOptions(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	contextDir string,
	dockerfile string,
	idOpts llbtodagger.DefinitionToIDOptions,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (*dagger.Container, dagger.ContainerID, string) {
	t.Helper()

	containerID, encoded := convertDockerfileToContainerIDWithOptions(ctx, t, c, contextDir, dockerfile, idOpts, optFns...)
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

	return convertDockerfileToContainerIDWithOptions(
		ctx,
		t,
		c,
		contextDir,
		dockerfile,
		llbtodagger.DefinitionToIDOptions{},
		optFns...,
	)
}

func convertDockerfileToContainerIDWithOptions(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	contextDir string,
	dockerfile string,
	idOpts llbtodagger.DefinitionToIDOptions,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (dagger.ContainerID, string) {
	t.Helper()

	def, img := dockerfileToDefinitionAndImage(ctx, t, contextDir, dockerfile, optFns...)
	id, err := llbtodagger.DefinitionToIDWithOptions(def, img, idOpts)
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

func llbStateToDefinition(
	ctx context.Context,
	t *testctx.T,
	st llb.State,
) *pb.Definition {
	t.Helper()

	def, err := st.Marshal(ctx)
	require.NoError(t, err)
	return def.ToPB()
}

func decodeCallID(t *testctx.T, encoded string) *call.ID {
	t.Helper()
	var id call.ID
	require.NoError(t, id.Decode(encoded))
	return &id
}

func findFieldInCallChain(id *call.ID, field string) *call.ID {
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == field {
			return cur
		}
	}
	return nil
}
