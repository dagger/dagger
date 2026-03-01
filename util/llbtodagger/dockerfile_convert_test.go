package llbtodagger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestDefinitionToIDDockerfileFromAndRun(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN echo hello
`)

	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 3)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withExec"))

	fromID := findFieldAnywhere(id, "from")
	require.NotNil(t, fromID)
	require.Contains(t, fromID.Arg("address").Value().ToInput(), "docker.io/library/alpine:3.19")

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	require.Equal(t, []any{"/bin/sh", "-c", "echo hello"}, withExec.Arg("args").Value().ToInput())
}

func TestDefinitionToIDDockerfileRunSecretMountAndEnv(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=secret,id=my-secret,env=MY_SECRET \
    --mount=type=secret,id=file-secret,target=/run/secrets/file-secret,uid=123,gid=456,mode=0440 \
    sh -c 'test -n "$MY_SECRET" && test -f /run/secrets/file-secret'
`)

	fileSecretID := fakeSecretID("file-secret")
	envSecretID := fakeSecretID("my-secret")
	id, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SecretIDsByLLBID: map[string]*call.ID{
			"file-secret": fileSecretID,
			"my-secret":   envSecretID,
		},
	})
	require.NoError(t, err)

	withSecretVariable := findFieldInChain(id, "withSecretVariable")
	require.NotNil(t, withSecretVariable)
	require.Equal(t, "MY_SECRET", withSecretVariable.Arg("name").Value().ToInput())

	withMountedSecret := findFieldInChain(id, "withMountedSecret")
	require.NotNil(t, withMountedSecret)
	require.Equal(t, "/run/secrets/file-secret", withMountedSecret.Arg("path").Value().ToInput())
	require.Equal(t, "123:456", withMountedSecret.Arg("owner").Value().ToInput())
	require.EqualValues(t, 0o440, withMountedSecret.Arg("mode").Value().ToInput())

	withoutSecretVariable := findFieldInChain(id, "withoutSecretVariable")
	require.NotNil(t, withoutSecretVariable)
	require.Equal(t, "MY_SECRET", withoutSecretVariable.Arg("name").Value().ToInput())

	withoutMount := findFieldInChain(id, "withoutMount")
	require.NotNil(t, withoutMount)
	require.Equal(t, "/run/secrets/file-secret", withoutMount.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileRunOptionalSecretsSkippedWhenMissing(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=secret,id=missing-env,env=MISSING_ENV \
    --mount=type=secret,id=missing-file,target=/run/secrets/missing \
    sh -c 'echo ok'
`)

	id, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SecretIDsByLLBID: map[string]*call.ID{},
	})
	require.NoError(t, err)

	require.Nil(t, findFieldInChain(id, "withSecretVariable"))
	require.Nil(t, findFieldInChain(id, "withMountedSecret"))
	require.Nil(t, findFieldInChain(id, "withoutSecretVariable"))
	require.Nil(t, findFieldInChain(id, "withoutMount"))
}

func TestDefinitionToIDDockerfileRunRequiredSecretMissingUnsupported(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=secret,id=required-secret,required=true,target=/run/secrets/required \
    sh -c 'cat /run/secrets/required'
`)

	_, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SecretIDsByLLBID: map[string]*call.ID{},
	})
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "required-secret")
	require.Contains(t, unsupportedErr.Reason, "required")
}

func TestDefinitionToIDDockerfileRunSSHMount(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=ssh,id=my-ssh,required=true,target=/tmp/agent.sock \
    sh -c 'test -S /tmp/agent.sock'
`)

	socketID := fakeSocketID("/tmp/source.sock")
	id, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SSHSocketIDsByLLBID: map[string]*call.ID{
			"my-ssh": socketID,
		},
	})
	require.NoError(t, err)

	withUnixSocket := findFieldInChain(id, "withUnixSocket")
	require.NotNil(t, withUnixSocket)
	require.Equal(t, "/tmp/agent.sock", withUnixSocket.Arg("path").Value().ToInput())
	require.Equal(t, "0:0", withUnixSocket.Arg("owner").Value().ToInput())

	withoutUnixSocket := findFieldInChain(id, "withoutUnixSocket")
	require.NotNil(t, withoutUnixSocket)
	require.Equal(t, "/tmp/agent.sock", withoutUnixSocket.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileRunOptionalSSHSkippedWhenMissing(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=ssh,id=missing,target=/tmp/agent.sock \
    sh -c 'echo ok'
`)

	id, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SSHSocketIDsByLLBID: map[string]*call.ID{},
	})
	require.NoError(t, err)

	require.Nil(t, findFieldInChain(id, "withUnixSocket"))
	require.Nil(t, findFieldInChain(id, "withoutUnixSocket"))
}

func TestDefinitionToIDDockerfileRunRequiredSSHMissingUnsupported(t *testing.T) {
	t.Parallel()

	def, img := dockerfileToDefinition(t, `
FROM alpine:3.19
RUN --mount=type=ssh,id=required-ssh,required=true,target=/tmp/agent.sock \
    sh -c 'test -S /tmp/agent.sock'
`)

	_, err := DefinitionToIDWithOptions(def, img, DefinitionToIDOptions{
		SSHSocketIDsByLLBID: map[string]*call.ID{},
	})
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "required-ssh")
	require.Contains(t, unsupportedErr.Reason, "required")
}

func TestDefinitionToIDDockerfileRunNetworkNoneMapsToWithExecNoNetwork(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN --network=none sh -c 'echo hello'
`)

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	noNetwork := withExec.Arg("noNetwork")
	require.NotNil(t, noNetwork)
	require.Equal(t, true, noNetwork.Value().ToInput())
}

func TestDefinitionToIDDockerfileRunNetworkHostMapsToWithExecHostNetwork(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN --network=host sh -c 'echo hello'
`)

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	hostNetwork := withExec.Arg("hostNetwork")
	require.NotNil(t, hostNetwork)
	require.Equal(t, true, hostNetwork.Value().ToInput())
}

func TestDefinitionToIDDockerfileRunNoInitOptionMapsToWithExecNoInit(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToIDWithDefinitionOptions(t, `
FROM alpine:3.19
RUN sh -c 'echo hello'
`, DefinitionToIDOptions{
		NoInit: true,
	})

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	noInit := withExec.Arg("noInit")
	require.NotNil(t, noInit)
	require.Equal(t, true, noInit.Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyFromContext(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
COPY . /app/
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/app", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	hostDir := findHostDirectoryCall(sourceID)
	require.NotNil(t, hostDir)
	require.Equal(t, "/workspace", hostDir.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyWithChmod(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
COPY --chmod=751 . /app/
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/app", withDir.Arg("path").Value().ToInput())
	require.EqualValues(t, 0o751, withDir.Arg("permissions").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyToExplicitFileDestWithChmod(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
COPY --chmod=751 input.txt /app/out.txt
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.Equal(t, "/app/out.txt", withFile.Arg("path").Value().ToInput())
	require.EqualValues(t, 0o751, withFile.Arg("permissions").Value().ToInput())
	allowDirFallback := withFile.Arg("allowDirectorySourceFallback")
	require.NotNil(t, allowDirFallback)
	require.Equal(t, true, allowDirFallback.Value().ToInput())

	srcFile := argIDFromCall(t, withFile, "source")
	fileCall := findFieldInChain(srcFile, "file")
	require.NotNil(t, fileCall)
	require.Equal(t, "input.txt", fileCall.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyGroupOnlyChown(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
COPY --chown=:123 input.txt /app/out.txt
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.Equal(t, "/app/out.txt", withFile.Arg("path").Value().ToInput())
	require.Equal(t, "0:123", withFile.Arg("owner").Value().ToInput())
	allowDirFallback := withFile.Arg("allowDirectorySourceFallback")
	require.NotNil(t, allowDirFallback)
	require.Equal(t, true, allowDirFallback.Value().ToInput())

	srcFile := argIDFromCall(t, withFile, "source")
	fileCall := findFieldInChain(srcFile, "file")
	require.NotNil(t, fileCall)
	require.Equal(t, "input.txt", fileCall.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyNamedChown(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN addgroup -g 4321 agroup && adduser -D -u 1234 -G agroup auser
COPY --chown=auser:agroup input.txt /app/out.txt
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withExec"))

	withFile := findFieldInChain(id, "withFile")
	require.NotNil(t, withFile)
	require.Equal(t, "/app/out.txt", withFile.Arg("path").Value().ToInput())
	require.Equal(t, "auser:agroup", withFile.Arg("owner").Value().ToInput())
	allowDirFallback := withFile.Arg("allowDirectorySourceFallback")
	require.NotNil(t, allowDirFallback)
	require.Equal(t, true, allowDirFallback.Value().ToInput())

	srcFile := argIDFromCall(t, withFile, "source")
	fileCall := findFieldInChain(srcFile, "file")
	require.NotNil(t, fileCall)
	require.Equal(t, "input.txt", fileCall.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileWorkdirNamedUserUsesContainerMkdirCompatibilityPath(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN addgroup -g 4321 appgrp && adduser -D -u 1234 -G appgrp app
USER app:appgrp
WORKDIR /work
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])

	mkdirCompat := findCallByStringArg(id, "withDirectory", "owner", "app:appgrp")
	require.NotNil(t, mkdirCompat)
	require.Equal(t, "/work", mkdirCompat.Arg("path").Value().ToInput())
	require.EqualValues(t, 0o755, mkdirCompat.Arg("permissions").Value().ToInput())

	sourceID := argIDFromCall(t, mkdirCompat, "source")
	sourceDir := findFieldInChain(sourceID, "directory")
	require.NotNil(t, sourceDir)
	require.Equal(t, mkdirCompatSyntheticSourcePath, sourceDir.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileAddHTTP(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
ADD https://example.com/pkg.tar.gz /downloads/
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/downloads", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	httpID := findFieldAnywhere(sourceID, "http")
	require.NotNil(t, httpID)
	require.Equal(t, "https://example.com/pkg.tar.gz", httpID.Arg("url").Value().ToInput())
	require.Equal(t, "pkg.tar.gz", httpID.Arg("name").Value().ToInput())
}

func TestDefinitionToIDDockerfileAddLocalArchiveUsesAttemptUnpackCompat(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
ADD archive.tar /downloads/
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/downloads", withDir.Arg("path").Value().ToInput())

	attemptUnpack := withDir.Arg("attemptUnpackDockerCompatibility")
	require.NotNil(t, attemptUnpack)
	require.Equal(t, true, attemptUnpack.Value().ToInput())

	requiredSourcePath := withDir.Arg("requiredSourcePath")
	require.NotNil(t, requiredSourcePath)
	require.Equal(t, "archive.tar", requiredSourcePath.Value().ToInput())

	destPathHintIsDirectory := withDir.Arg("destPathHintIsDirectory")
	require.NotNil(t, destPathHintIsDirectory)
	require.Equal(t, true, destPathHintIsDirectory.Value().ToInput())
}

func TestDefinitionToIDDockerfileAddGit(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
ADD https://github.com/dagger/dagger.git#main /vendor/dagger/
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/vendor/dagger", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	gitID := findFieldAnywhere(sourceID, "git")
	require.NotNil(t, gitID)
	require.Contains(t, gitID.Arg("url").Value().ToInput(), "github.com/dagger/dagger")

	refID := findFieldAnywhere(sourceID, "ref")
	require.NotNil(t, refID)
	require.Equal(t, "main", refID.Arg("name").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyDirContentsToRootUsesSourceSubdir(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19 AS buildfull
RUN mkdir -p /out/lib/systemd/system && echo svc >/out/lib/systemd/system/containerd.service
FROM alpine:3.19 AS outfull
COPY --from=buildfull /out /
`)

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))

	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, "withDirectory", withDir.Field())
	require.Equal(t, "/", withDir.Arg("path").Value().ToInput())
	require.Equal(t, []any{"out"}, withDir.Arg("include").Value().ToInput())
	copySourcePathContentsWhenDir := withDir.Arg("copySourcePathContentsWhenDir")
	require.NotNil(t, copySourcePathContentsWhenDir)
	require.Equal(t, true, copySourcePathContentsWhenDir.Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	require.Equal(t, "rootfs", sourceID.Field())
}

func TestDockerfile2LLBCopyOutToRootFlags(t *testing.T) {
	t.Parallel()

	def, _ := dockerfileToDefinition(t, `
FROM alpine:3.19 AS buildfull
RUN mkdir -p /out/lib/systemd/system && echo svc >/out/lib/systemd/system/containerd.service
FROM alpine:3.19 AS outfull
COPY --from=buildfull /out /
`)

	dag, err := buildkit.DefToDAG(def)
	require.NoError(t, err)
	require.NotNil(t, dag)

	var found *pb.FileActionCopy
	err = dag.Walk(func(op *buildkit.OpDAG) error {
		fileOp, ok := op.AsFile()
		if !ok {
			return nil
		}
		for _, action := range fileOp.Actions {
			cp := action.GetCopy()
			if cp == nil {
				continue
			}
			if cp.Dest == "/" {
				found = cp
				return buildkit.SkipInputs
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "/out", found.Src)
	require.True(t, found.AllowWildcard)
	require.True(t, found.DirCopyContents)
}

func TestDockerfile2LLBCopyFileToExplicitDestFlags(t *testing.T) {
	t.Parallel()

	def, _ := dockerfileToDefinition(t, `
FROM alpine:3.19
COPY input.txt /app/out.txt
`)

	dag, err := buildkit.DefToDAG(def)
	require.NoError(t, err)
	require.NotNil(t, dag)

	var found *pb.FileActionCopy
	err = dag.Walk(func(op *buildkit.OpDAG) error {
		fileOp, ok := op.AsFile()
		if !ok {
			return nil
		}
		for _, action := range fileOp.Actions {
			cp := action.GetCopy()
			if cp == nil {
				continue
			}
			if cp.Dest == "/app/out.txt" {
				found = cp
				return buildkit.SkipInputs
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "/input.txt", found.Src)
	require.True(t, found.AllowWildcard)
	require.True(t, found.DirCopyContents)
}

func TestDefinitionToIDDockerfileCopyLink(t *testing.T) {
	t.Parallel()

	caps := pb.Caps.CapSet(pb.Caps.All())
	id := convertDockerfileToIDWithOpt(t, `
FROM scratch
COPY --link . /linked/
`, func(opt *dockerfile2llb.ConvertOpt) {
		opt.LLBCaps = &caps
	})

	fields := fieldsFromRoot(id)
	require.NotEmpty(t, fields)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withRootfs"))
	copyCall := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(copyCall))
	require.Equal(t, "/linked", copyCall.Arg("path").Value().ToInput())
	require.NotNil(t, findHostDirectoryCall(argIDFromCall(t, copyCall, "source")))
}

func TestDefinitionToIDDockerfileComplexCombination(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19 AS base
WORKDIR /work
COPY . .
ADD https://example.com/artifact.tgz /work/vendor/
ADD https://github.com/dagger/dagger.git#main /work/vendor/dagger/
RUN echo base

FROM alpine:3.19
WORKDIR /final
RUN addgroup -g 4321 agroup && adduser -D -u 1234 -G agroup auser
COPY --from=base /work /out/work
COPY --chown=auser:agroup --from=base /work /out/owned
RUN echo done
`)

	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 3)
	require.Equal(t, "container", fields[0])
	require.NotNil(t, findFieldInChain(id, "withExec"))

	finalExec := findFieldInChain(id, "withExec")
	require.NotNil(t, finalExec)
	require.Equal(t, []any{"/bin/sh", "-c", "echo done"}, finalExec.Arg("args").Value().ToInput())

	require.NotNil(t, findFieldAnywhere(id, "host"))
	require.NotNil(t, findFieldAnywhere(id, "http"))
	require.NotNil(t, findFieldAnywhere(id, "git"))
	require.NotNil(t, findFieldAnywhere(id, "withNewDirectory"))

	namedOwnerCopy := findCallByStringArg(id, "withDirectory", "owner", "auser:agroup")
	if namedOwnerCopy == nil {
		namedOwnerCopy = findCallByStringArg(id, "withFile", "owner", "auser:agroup")
	}
	require.NotNil(t, namedOwnerCopy)
	pathArg := namedOwnerCopy.Arg("path")
	require.NotNil(t, pathArg)
	require.Contains(t, fmt.Sprint(pathArg.Value().ToInput()), "/out/owned")

	finalWorkdir := findFieldInChain(id, "withWorkdir")
	require.NotNil(t, finalWorkdir)
	require.Equal(t, "/final", finalWorkdir.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileImageMetadata(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
ENV FOO=bar
USER 1234
WORKDIR /workspace
LABEL com.example.name=demo
EXPOSE 8080
EXPOSE 9090/udp
ENTRYPOINT ["sh","-c"]
CMD ["echo","hi"]
`)

	require.Equal(t, "container", fieldsFromRoot(id)[0])

	userCall := findCallByStringArg(id, "withUser", "name", "1234")
	require.NotNil(t, userCall)

	workdirCall := findCallByStringArg(id, "withWorkdir", "path", "/workspace")
	require.NotNil(t, workdirCall)

	envCall := findCallByStringArg(id, "withEnvVariable", "name", "FOO")
	require.NotNil(t, envCall)
	require.Equal(t, "bar", envCall.Arg("value").Value().ToInput())

	labelCall := findCallByStringArg(id, "withLabel", "name", "com.example.name")
	require.NotNil(t, labelCall)
	require.Equal(t, "demo", labelCall.Arg("value").Value().ToInput())

	entrypointCall := findFieldInChain(id, "withEntrypoint")
	require.NotNil(t, entrypointCall)
	require.Equal(t, []any{"sh", "-c"}, entrypointCall.Arg("args").Value().ToInput())
	require.Equal(t, true, entrypointCall.Arg("keepDefaultArgs").Value().ToInput())

	cmdCall := findFieldInChain(id, "withDefaultArgs")
	require.NotNil(t, cmdCall)
	require.Equal(t, []any{"echo", "hi"}, cmdCall.Arg("args").Value().ToInput())

	exposedPorts := findCallsInChain(id, "withExposedPort")
	require.Len(t, exposedPorts, 2)
	foundPorts := map[string]bool{}
	for _, portCall := range exposedPorts {
		key := fmt.Sprintf("%v/%v", portCall.Arg("port").Value().ToInput(), portCall.Arg("protocol").Value().ToInput())
		foundPorts[key] = true
	}
	require.True(t, foundPorts["8080/TCP"])
	require.True(t, foundPorts["9090/UDP"])
}

func TestDefinitionToIDDockerfileInternalImageMetadataFields(t *testing.T) {
	t.Parallel()

	dockerfile := `
FROM alpine:3.19
RUN echo hi > /out.txt
HEALTHCHECK --interval=21s --timeout=4s --start-period=9s --start-interval=2s --retries=5 CMD ["sh","-c","test -f /out.txt"]
ONBUILD RUN echo child-build
SHELL ["/bin/ash","-eo","pipefail","-c"]
VOLUME ["/cache","/data"]
STOPSIGNAL SIGQUIT
`
	def, img := dockerfileToDefinition(t, dockerfile)
	id, err := DefinitionToID(def, img)
	require.NoError(t, err)

	metaCall := findFieldInChain(id, "__withImageConfigMetadata")
	require.NotNil(t, metaCall)

	healthcheckArg := metaCall.Arg("healthcheck")
	require.NotNil(t, healthcheckArg)
	healthcheckJSON, ok := healthcheckArg.Value().ToInput().(string)
	require.True(t, ok)

	var healthcheck dockerspec.HealthcheckConfig
	require.NoError(t, json.Unmarshal([]byte(healthcheckJSON), &healthcheck))
	require.NotNil(t, img.Config.Healthcheck)
	require.Equal(t, *img.Config.Healthcheck, healthcheck)

	onBuildArg := metaCall.Arg("onBuild")
	require.NotNil(t, onBuildArg)
	require.Equal(t, stringSlice(onBuildArg.Value().ToInput()), img.Config.OnBuild)

	shellArg := metaCall.Arg("shell")
	require.NotNil(t, shellArg)
	require.Equal(t, stringSlice(shellArg.Value().ToInput()), img.Config.Shell)

	volumesArg := metaCall.Arg("volumes")
	require.NotNil(t, volumesArg)
	require.Equal(t, sortedStringKeys(img.Config.Volumes), stringSlice(volumesArg.Value().ToInput()))

	stopSignalArg := metaCall.Arg("stopSignal")
	require.NotNil(t, stopSignalArg)
	require.Equal(t, img.Config.StopSignal, stopSignalArg.Value().ToInput())

	// Sanity check: parsed healthcheck still has the expected timing payload.
	require.Equal(t, 21*time.Second, healthcheck.Interval)
	require.Equal(t, 4*time.Second, healthcheck.Timeout)
	require.Equal(t, 9*time.Second, healthcheck.StartPeriod)
	require.Equal(t, 2*time.Second, healthcheck.StartInterval)
	require.Equal(t, 5, healthcheck.Retries)
}

func TestDefinitionToIDDockerfileDeterministicEncoding(t *testing.T) {
	t.Parallel()

	dockerfile := `
FROM alpine:3.19
WORKDIR /work
COPY . .
RUN echo deterministic
`
	idA := convertDockerfileToID(t, dockerfile)
	idB := convertDockerfileToID(t, dockerfile)

	encA, err := idA.Encode()
	require.NoError(t, err)
	encB, err := idB.Encode()
	require.NoError(t, err)

	require.Equal(t, idA.Digest(), idB.Digest())
	require.Equal(t, encA, encB)
}

func convertDockerfileToID(t *testing.T, dockerfile string) *call.ID {
	t.Helper()
	return convertDockerfileToIDWithOpt(t, dockerfile)
}

func convertDockerfileToIDWithOpt(
	t *testing.T,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) *call.ID {
	t.Helper()

	def, img := dockerfileToDefinition(t, dockerfile, optFns...)
	id, err := DefinitionToID(def, img)
	require.NoError(t, err)
	return id
}

func convertDockerfileToIDWithDefinitionOptions(
	t *testing.T,
	dockerfile string,
	idOpts DefinitionToIDOptions,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) *call.ID {
	t.Helper()

	def, img := dockerfileToDefinition(t, dockerfile, optFns...)
	id, err := DefinitionToIDWithOptions(def, img, idOpts)
	require.NoError(t, err)
	return id
}

func dockerfileToDefinition(
	t *testing.T,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) (*pb.Definition, *dockerspec.DockerOCIImage) {
	t.Helper()

	mainContext := llb.Local("context", llb.SharedKeyHint("/workspace"))
	opt := dockerfile2llb.ConvertOpt{
		MainContext:    &mainContext,
		TargetPlatform: &ocispecs.Platform{OS: "linux", Architecture: "amd64"},
		MetaResolver:   staticImageMetaResolver{},
	}
	for _, fn := range optFns {
		fn(&opt)
	}

	st, img, _, _, err := dockerfile2llb.Dockerfile2LLB(context.Background(), []byte(strings.TrimSpace(dockerfile)), opt)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.NotNil(t, img)

	def, err := st.Marshal(context.Background())
	require.NoError(t, err)
	return def.ToPB(), img
}

type staticImageMetaResolver struct{}

func (staticImageMetaResolver) ResolveImageConfig(context.Context, string, sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	imgCfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{digest.FromString("static-layer").String()},
		},
		"config": map[string]any{
			"Env": []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		},
	}
	dt, err := json.Marshal(imgCfg)
	if err != nil {
		return "", "", nil, err
	}
	return "docker.io/library/alpine:3.19", digest.FromString("static-config"), dt, nil
}

func argIDFromCall(t *testing.T, id *call.ID, argName string) *call.ID {
	t.Helper()
	arg := id.Arg(argName)
	require.NotNil(t, arg)
	argID, ok := arg.Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.NotNil(t, argID)
	return argID
}

func findHostDirectoryCall(id *call.ID) *call.ID {
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == "directory" && cur.Receiver() != nil && cur.Receiver().Field() == "host" {
			return cur
		}
	}
	return nil
}

func findFieldAnywhere(root *call.ID, field string) *call.ID {
	seen := map[digest.Digest]struct{}{}
	var visitID func(*call.ID) *call.ID
	var visitAny func(any) *call.ID

	visitAny = func(v any) *call.ID {
		switch x := v.(type) {
		case *call.ID:
			return visitID(x)
		case []any:
			for _, elem := range x {
				if found := visitAny(elem); found != nil {
					return found
				}
			}
		case map[string]any:
			for _, elem := range x {
				if found := visitAny(elem); found != nil {
					return found
				}
			}
		}
		return nil
	}

	visitID = func(id *call.ID) *call.ID {
		if id == nil {
			return nil
		}
		if _, ok := seen[id.Digest()]; ok {
			return nil
		}
		seen[id.Digest()] = struct{}{}

		if id.Field() == field {
			return id
		}
		if found := visitID(id.Receiver()); found != nil {
			return found
		}
		for _, arg := range id.Args() {
			if found := visitAny(arg.Value().ToInput()); found != nil {
				return found
			}
		}
		return nil
	}

	return visitID(root)
}

func findCallsInChain(id *call.ID, field string) []*call.ID {
	var ids []*call.ID
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == field {
			ids = append(ids, cur)
		}
	}
	return ids
}

func findCallByStringArg(id *call.ID, field, argName, argValue string) *call.ID {
	for _, match := range findCallsInChain(id, field) {
		arg := match.Arg(argName)
		if arg == nil {
			continue
		}
		if val, ok := arg.Value().ToInput().(string); ok && val == argValue {
			return match
		}
	}
	return nil
}

func stringSlice(val any) []string {
	list, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if s, ok := entry.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedStringKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
