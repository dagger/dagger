package llbtodagger

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestDefinitionToIDScratch(t *testing.T) {
	t.Parallel()

	id, err := DefinitionToID(&pb.Definition{}, nil)
	require.NoError(t, err)
	require.NotNil(t, id)
	require.Equal(t, []string{"container"}, fieldsFromRoot(id))
}

func TestDefinitionToIDImageRootFS(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine")
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)

	id, err := DefinitionToID(def.ToPB(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "from"}, fieldsFromRoot(id))

	fromID := id
	require.NotNil(t, fromID)
	require.Equal(t, "from", fromID.Field())
	require.Equal(t, "docker.io/library/alpine:latest", fromID.Arg("address").Value().ToInput())
}

func TestDefinitionToIDExecRootfs(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(llb.Shlex("echo hello")).Root()
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)

	id, err := DefinitionToID(def.ToPB(), nil)
	require.NoError(t, err)
	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 3)
	require.Equal(t, "container", fields[0])
	require.Equal(t, "withExec", fields[len(fields)-1])

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	require.Equal(t, []any{"echo", "hello"}, withExec.Arg("args").Value().ToInput())
}

func TestDefinitionToIDFileMkfile(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().File(llb.Mkfile("/hello.txt", 0644, []byte("hello")))
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)

	id, err := DefinitionToID(def.ToPB(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	rootfsID := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withNewFile"}, fieldsFromRoot(rootfsID))
}

func TestDefinitionToIDBuildUnsupported(t *testing.T) {
	t.Parallel()

	op := &pb.Op{Op: &pb.Op_Build{Build: &pb.BuildOp{Def: &pb.Definition{}}}}
	_, err := DefinitionToID(singleOpDefinition(t, op), nil)
	unsupportedErr := requireUnsupported(t, err, "build")
	require.Contains(t, unsupportedErr.Reason, "explicitly unsupported")
}

func TestDefinitionToIDBlobUnsupported(t *testing.T) {
	t.Parallel()

	st := blob.LLB(digest.FromString("blob-data"))
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)

	_, err = DefinitionToID(def.ToPB(), nil)
	unsupportedErr := requireUnsupported(t, err, "source(blob)")
	require.Contains(t, unsupportedErr.Reason, "explicitly unsupported")
}

func TestDefinitionToIDGitSource(t *testing.T) {
	t.Parallel()

	st := llb.Git("https://github.com/dagger/dagger", "main")
	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	rootfsID := rootfsArgFromContainer(t, id)

	gitCall := findFieldInChain(rootfsID, "git")
	require.NotNil(t, gitCall)
	require.Equal(t, "https://github.com/dagger/dagger", gitCall.Arg("url").Value().ToInput())

	refCall := findFieldInChain(rootfsID, "ref")
	require.NotNil(t, refCall)
	require.Equal(t, "main", refCall.Arg("name").Value().ToInput())
}

func TestDefinitionToIDLocalSource(t *testing.T) {
	t.Parallel()

	st := llb.Local(
		"ctx",
		llb.SharedKeyHint("/workspace"),
		llb.IncludePatterns([]string{"*.go"}),
		llb.ExcludePatterns([]string{"*_test.go"}),
	)
	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	rootfsID := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"host", "directory"}, fieldsFromRoot(rootfsID))

	dirCall := rootfsID
	require.Equal(t, "/workspace", dirCall.Arg("path").Value().ToInput())
	require.Equal(t, []any{"*.go"}, dirCall.Arg("include").Value().ToInput())
	require.Equal(t, []any{"*_test.go"}, dirCall.Arg("exclude").Value().ToInput())
}

func TestDefinitionToIDLocalFollowPaths(t *testing.T) {
	t.Parallel()

	st := llb.Local(
		"ctx",
		llb.SharedKeyHint("/workspace"),
		llb.FollowPaths([]string{"foo"}),
	)
	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	rootfsID := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"host", "directory"}, fieldsFromRoot(rootfsID))
	require.Equal(t, "/workspace", rootfsID.Arg("path").Value().ToInput())
	require.Equal(t, []any{"foo"}, rootfsID.Arg("followPaths").Value().ToInput())
}

func TestDefinitionToIDLocalFollowPathsInvalidUnsupported(t *testing.T) {
	t.Parallel()

	op := &pb.Op{
		Op: &pb.Op_Source{
			Source: &pb.SourceOp{
				Identifier: "local://ctx",
				Attrs: map[string]string{
					pb.AttrSharedKeyHint: "/workspace",
					pb.AttrFollowPaths:   "{not-json",
				},
			},
		},
	}

	_, err := DefinitionToID(singleOpDefinition(t, op), nil)
	unsupportedErr := requireUnsupported(t, err, "source(local)")
	require.Contains(t, unsupportedErr.Reason, "invalid follow paths")
}

func TestDefinitionToIDLocalMainContextSentinelRebinding(t *testing.T) {
	t.Parallel()

	st := DockerfileMainContextSentinelState()
	contextDirID := appendCall(call.New(), directoryType(), "directory", argString("path", "/rebased-context"))

	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{MainContextDirectoryID: contextDirID},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	rootfsID := rootfsArgFromContainer(t, id)
	require.Equal(t, contextDirID.Display(), rootfsID.Display())
	require.Equal(t, []string{"directory"}, fieldsFromRoot(rootfsID))
	require.Equal(t, "/rebased-context", rootfsID.Arg("path").Value().ToInput())
	require.NotContains(t, rootfsID.Display(), DockerfileMainContextSentinelLocalName)
	require.NotContains(t, rootfsID.Display(), DockerfileMainContextSentinelSharedKeyHint)
}

func TestDefinitionToIDLocalMainContextSentinelMissingRebindingUnsupported(t *testing.T) {
	t.Parallel()

	st := DockerfileMainContextSentinelState()
	_, err := DefinitionToIDWithOptions(marshalStateToPB(t, st), nil, DefinitionToIDOptions{})
	unsupportedErr := requireUnsupported(t, err, "source(local)")
	require.Contains(t, unsupportedErr.Reason, "main-context sentinel")
	require.Contains(t, unsupportedErr.Reason, "rebinding")
}

func TestDefinitionToIDHTTPSource(t *testing.T) {
	t.Parallel()

	st := llb.HTTP("https://example.com/archive.tgz", llb.Filename("payload.tgz"), llb.Chmod(0o640))
	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.Equal(t, "payload.tgz", withFile.Arg("path").Value().ToInput())
	sourceArg := withFile.Arg("source")
	require.NotNil(t, sourceArg)
	sourceID, ok := sourceArg.Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, "http", sourceID.Field())
	require.Equal(t, "https://example.com/archive.tgz", sourceID.Arg("url").Value().ToInput())
	require.Equal(t, "payload.tgz", sourceID.Arg("name").Value().ToInput())
	require.Equal(t, int64(0o640), sourceID.Arg("permissions").Value().ToInput())
}

func TestDefinitionToIDHTTPChecksumUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.HTTP("https://example.com/archive.tgz", llb.Checksum(digest.FromString("checksum")))
	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "source(http)")
	require.Contains(t, unsupportedErr.Reason, "checksum")
}

func TestDefinitionToIDExecMounts(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkfile("/input.txt", 0o644, []byte("hello")))
	st := llb.Image("alpine").Run(
		llb.Args([]string{"echo", "hello"}),
		llb.AddMount("/work", src),
		llb.AddMount("/cache", llb.Scratch(), llb.AsPersistentCacheDir("cache-key", llb.CacheMountPrivate)),
		llb.AddMount("/tmp", llb.Scratch(), llb.Tmpfs(llb.TmpfsSize(2048))),
	).Root()

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 8)
	require.Equal(t, "container", fields[0])
	require.Equal(t, "withoutMount", fields[len(fields)-1])

	withRootfs := findFieldInChain(id, "withRootfs")
	require.Nil(t, withRootfs)
	require.NotNil(t, findFieldInChain(id, "from"))

	withMountDir := findFieldInChain(id, "withMountedDirectory")
	require.NotNil(t, withMountDir)
	require.Equal(t, "/work", withMountDir.Arg("path").Value().ToInput())

	withCache := findFieldInChain(id, "withMountedCache")
	require.NotNil(t, withCache)
	require.Equal(t, "/cache", withCache.Arg("path").Value().ToInput())
	require.Equal(t, "PRIVATE", withCache.Arg("sharing").Value().ToInput())
	cacheID, ok := withCache.Arg("cache").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, "cacheVolume", cacheID.Field())
	require.Equal(t, "cache-key", cacheID.Arg("key").Value().ToInput())

	withTemp := findFieldInChain(id, "withMountedTemp")
	require.NotNil(t, withTemp)
	require.Equal(t, "/tmp", withTemp.Arg("path").Value().ToInput())
	require.Equal(t, int64(2048), withTemp.Arg("size").Value().ToInput())

	withoutMounts := findFieldsInChain(id, "withoutMount")
	require.Len(t, withoutMounts, 3)
	gotPaths := map[any]bool{}
	for _, withoutMount := range withoutMounts {
		gotPaths[withoutMount.Arg("path").Value().ToInput()] = true
	}
	require.True(t, gotPaths["/work"])
	require.True(t, gotPaths["/cache"])
	require.True(t, gotPaths["/tmp"])
}

func TestDefinitionToIDExecReadonlyBindMount(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddMount("/ro", llb.Scratch(), llb.Readonly),
	).Root()

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	withMountDir := findFieldInChain(id, "withMountedDirectory")
	require.NotNil(t, withMountDir)
	require.Equal(t, "/ro", withMountDir.Arg("path").Value().ToInput())
	require.Equal(t, true, withMountDir.Arg("readOnly").Value().ToInput())

	withoutMount := findFieldInChain(id, "withoutMount")
	require.NotNil(t, withoutMount)
	require.Equal(t, "/ro", withoutMount.Arg("path").Value().ToInput())
}

func TestDefinitionToIDExecMountsAreNonStickyBetweenExecs(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkfile("/input.txt", 0o644, []byte("hello")))
	st := llb.Image("alpine").
		Run(
			llb.Shlex("cat /work/input.txt > /out.txt"),
			llb.AddMount("/work", src),
		).
		Run(
			llb.Shlex("test ! -e /work/input.txt"),
		).
		Root()

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	fields := fieldsFromRoot(id)
	withExecIdx := fieldIndices(fields, "withExec")
	require.Len(t, withExecIdx, 2)
	withoutMountIdx := fieldIndices(fields, "withoutMount")
	require.Len(t, withoutMountIdx, 1)
	require.Greater(t, withoutMountIdx[0], withExecIdx[0])
	require.Less(t, withoutMountIdx[0], withExecIdx[1])

	withoutMount := findFieldInChain(id, "withoutMount")
	require.NotNil(t, withoutMount)
	require.Equal(t, "/work", withoutMount.Arg("path").Value().ToInput())
}

func TestDefinitionToIDExecNetworkUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Network(llb.NetModeHost).Run(llb.Shlex("echo hello")).Root()
	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "network mode")
}

func TestDefinitionToIDExecSecretRequiredMissingMappingUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSecret("/run/secrets/token"),
	).Root()

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "required")
	require.Contains(t, unsupportedErr.Reason, "no secret mappings")
}

func TestDefinitionToIDExecSecretMountAndEnv(t *testing.T) {
	t.Parallel()

	mountPath := "/run/secrets/token"
	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSecretWithDest("mount-secret", &mountPath, llb.SecretFileOpt(123, 456, 0o440)),
		llb.AddSecretWithDest("env-secret", nil, llb.SecretAsEnvName("TOKEN")),
	).Root()

	mountSecretID := fakeSecretID("mount-secret")
	envSecretID := fakeSecretID("env-secret")
	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{
			SecretIDsByLLBID: map[string]*call.ID{
				"mount-secret": mountSecretID,
				"env-secret":   envSecretID,
			},
		},
	)
	require.NoError(t, err)

	withSecretVariable := findFieldInChain(id, "withSecretVariable")
	require.NotNil(t, withSecretVariable)
	require.Equal(t, "TOKEN", withSecretVariable.Arg("name").Value().ToInput())
	gotEnvSecretID, ok := withSecretVariable.Arg("secret").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, envSecretID.Display(), gotEnvSecretID.Display())

	withMountedSecret := findFieldInChain(id, "withMountedSecret")
	require.NotNil(t, withMountedSecret)
	require.Equal(t, mountPath, withMountedSecret.Arg("path").Value().ToInput())
	require.Equal(t, "123:456", withMountedSecret.Arg("owner").Value().ToInput())
	require.EqualValues(t, 0o440, withMountedSecret.Arg("mode").Value().ToInput())
	gotMountedSecretID, ok := withMountedSecret.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, mountSecretID.Display(), gotMountedSecretID.Display())

	withoutMount := findFieldInChain(id, "withoutMount")
	require.NotNil(t, withoutMount)
	require.Equal(t, mountPath, withoutMount.Arg("path").Value().ToInput())

	withoutSecretVariable := findFieldInChain(id, "withoutSecretVariable")
	require.NotNil(t, withoutSecretVariable)
	require.Equal(t, "TOKEN", withoutSecretVariable.Arg("name").Value().ToInput())
}

func TestDefinitionToIDExecOptionalSecretsAreSkippedWhenMissing(t *testing.T) {
	t.Parallel()

	mountPath := "/run/secrets/optional-mount"
	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSecretWithDest("optional-mount", &mountPath, llb.SecretOptional),
		llb.AddSecretWithDest("optional-env", nil, llb.SecretAsEnvName("OPTIONAL_SECRET"), llb.SecretOptional),
	).Root()

	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{SecretIDsByLLBID: map[string]*call.ID{}},
	)
	require.NoError(t, err)

	require.Nil(t, findFieldInChain(id, "withMountedSecret"))
	require.Nil(t, findFieldInChain(id, "withSecretVariable"))
	require.Nil(t, findFieldInChain(id, "withoutMount"))
	require.Nil(t, findFieldInChain(id, "withoutSecretVariable"))
}

func TestDefinitionToIDExecSSHRequiredMissingMappingUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSSHSocket(
			llb.SSHID("required-ssh"),
			llb.SSHSocketTarget("/run/buildkit/ssh_agent.0"),
		),
	).Root()

	_, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{SSHSocketIDsByLLBID: map[string]*call.ID{}},
	)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "required-ssh")
	require.Contains(t, unsupportedErr.Reason, "required")
}

func TestDefinitionToIDExecSSHMountAndCleanup(t *testing.T) {
	t.Parallel()

	sshPath := "/tmp/ssh-agent.sock"
	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSSHSocket(
			llb.SSHID("build-ssh"),
			llb.SSHSocketOpt(sshPath, 123, 456, 0o600),
		),
	).Root()

	sshSocketID := fakeSocketID("build-ssh")
	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{
			SSHSocketIDsByLLBID: map[string]*call.ID{
				"build-ssh": sshSocketID,
			},
		},
	)
	require.NoError(t, err)

	withUnixSocket := findFieldInChain(id, "withUnixSocket")
	require.NotNil(t, withUnixSocket)
	require.Equal(t, sshPath, withUnixSocket.Arg("path").Value().ToInput())
	require.Equal(t, "123:456", withUnixSocket.Arg("owner").Value().ToInput())
	gotSocketID, ok := withUnixSocket.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, sshSocketID.Display(), gotSocketID.Display())

	withoutUnixSocket := findFieldInChain(id, "withoutUnixSocket")
	require.NotNil(t, withoutUnixSocket)
	require.Equal(t, sshPath, withoutUnixSocket.Arg("path").Value().ToInput())
}

func TestDefinitionToIDExecOptionalSSHIsSkippedWhenMissing(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSSHSocket(
			llb.SSHID("optional-ssh"),
			llb.SSHSocketTarget("/run/buildkit/ssh_agent.0"),
			llb.SSHOptional,
		),
	).Root()

	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{SSHSocketIDsByLLBID: map[string]*call.ID{}},
	)
	require.NoError(t, err)
	require.Nil(t, findFieldInChain(id, "withUnixSocket"))
	require.Nil(t, findFieldInChain(id, "withoutUnixSocket"))
}

func TestDefinitionToIDExecSSHUsesDefaultMapping(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSSHSocket(
			llb.SSHID("custom-id"),
			llb.SSHSocketTarget("/run/buildkit/ssh_agent.0"),
		),
	).Root()

	defaultSocketID := fakeSocketID("default-ssh")
	id, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{
			SSHSocketIDsByLLBID: map[string]*call.ID{
				"": defaultSocketID,
			},
		},
	)
	require.NoError(t, err)

	withUnixSocket := findFieldInChain(id, "withUnixSocket")
	require.NotNil(t, withUnixSocket)
	gotSocketID, ok := withUnixSocket.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, defaultSocketID.Display(), gotSocketID.Display())
}

func TestDefinitionToIDExecSSHModeUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSSHSocket(
			llb.SSHID("bad-mode"),
			llb.SSHSocketOpt("/run/buildkit/ssh_agent.0", 0, 0, 0o644),
		),
	).Root()

	_, err := DefinitionToIDWithOptions(
		marshalStateToPB(t, st),
		nil,
		DefinitionToIDOptions{
			SSHSocketIDsByLLBID: map[string]*call.ID{
				"bad-mode": fakeSocketID("bad-mode"),
			},
		},
	)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "mode")
}

func TestDefinitionToIDFileCopy(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{CreateDestPath: true},
			llb.WithExcludePatterns([]string{"*.tmp"}),
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/dst/a.txt", withDir.Arg("path").Value().ToInput())
	require.Equal(t, []any{"a.txt"}, withDir.Arg("include").Value().ToInput())
	require.Equal(t, []any{"*.tmp"}, withDir.Arg("exclude").Value().ToInput())
	sourceID, ok := withDir.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.Equal(t, "directory", sourceID.Field())
	require.Equal(t, "/src", sourceID.Arg("path").Value().ToInput())
}

func TestDefinitionToIDFileCopyDoNotCreateDestPathWithDirectory(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/",
			&llb.CopyInfo{CreateDestPath: false},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	doNotCreateDestPath := withDir.Arg("doNotCreateDestPath")
	require.NotNil(t, doNotCreateDestPath)
	require.Equal(t, true, doNotCreateDestPath.Value().ToInput())
	requiredSourcePath := withDir.Arg("requiredSourcePath")
	require.NotNil(t, requiredSourcePath)
	require.Equal(t, "a.txt", requiredSourcePath.Value().ToInput())
}

func TestDefinitionToIDFileCopyDoNotCreateDestPathWithFile(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/renamed.txt",
			&llb.CopyInfo{CreateDestPath: false},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	doNotCreateDestPath := withFile.Arg("doNotCreateDestPath")
	require.NotNil(t, doNotCreateDestPath)
	require.Equal(t, true, doNotCreateDestPath.Value().ToInput())
}

func TestDefinitionToIDFileCopyModeOverride(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	mode := os.FileMode(0o751)
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{CreateDestPath: true, Mode: &mode},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "withRootfs"}, fieldsFromRoot(id))

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.EqualValues(t, 0o751, withFile.Arg("permissions").Value().ToInput())
	require.Equal(t, "/dst/a.txt", withFile.Arg("path").Value().ToInput())

	sourceID, ok := withFile.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	sourceFile := findFieldInChain(sourceID, "file")
	require.NotNil(t, sourceFile)
	require.Equal(t, "a.txt", sourceFile.Arg("path").Value().ToInput())
}

func TestDefinitionToIDFileCopyExplicitDestPathUsesWithFile(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/renamed.txt",
			&llb.CopyInfo{CreateDestPath: true},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.Equal(t, "/dst/renamed.txt", withFile.Arg("path").Value().ToInput())

	sourceID, ok := withFile.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	sourceFile := findFieldInChain(sourceID, "file")
	require.NotNil(t, sourceFile)
	require.Equal(t, "a.txt", sourceFile.Arg("path").Value().ToInput())
}

func TestDefinitionToIDFileCopyAttemptUnpackUsesWithDirectoryHiddenArg(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/archive.tar", 0o644, []byte("archive-data")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/archive.tar",
			"/dst/out.txt",
			&llb.CopyInfo{CreateDestPath: true, AttemptUnpack: true},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	withDir := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(withDir))
	require.Equal(t, "/dst/out.txt", withDir.Arg("path").Value().ToInput())

	attemptUnpack := withDir.Arg("attemptUnpackDockerCompatibility")
	require.NotNil(t, attemptUnpack)
	require.Equal(t, true, attemptUnpack.Value().ToInput())

	requiredSourcePath := withDir.Arg("requiredSourcePath")
	require.NotNil(t, requiredSourcePath)
	require.Equal(t, "archive.tar", requiredSourcePath.Value().ToInput())
}

func TestChownOwnerStringGroupOnlyByID(t *testing.T) {
	t.Parallel()

	owner, err := chownOwnerString(&pb.ChownOpt{
		Group: &pb.UserOpt{
			User: &pb.UserOpt_ByID{ByID: 123},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "0:123", owner)
}

func TestChownOwnerStringGroupOnlyWithEmptyUserName(t *testing.T) {
	t.Parallel()

	owner, err := chownOwnerString(&pb.ChownOpt{
		User: &pb.UserOpt{
			User: &pb.UserOpt_ByName{
				ByName: &pb.NamedUserOpt{Name: ""},
			},
		},
		Group: &pb.UserOpt{
			User: &pb.UserOpt_ByID{ByID: 456},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "0:456", owner)
}

func TestChownOwnerStringNamedUserGroup(t *testing.T) {
	t.Parallel()

	owner, err := chownOwnerString(&pb.ChownOpt{
		User: &pb.UserOpt{
			User: &pb.UserOpt_ByName{
				ByName: &pb.NamedUserOpt{Name: "builder"},
			},
		},
		Group: &pb.UserOpt{
			User: &pb.UserOpt_ByName{
				ByName: &pb.NamedUserOpt{Name: "staff"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "builder:staff", owner)
}

func TestDefinitionToIDFileCopyNamedChownWithoutContainerContextUnsupported(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().
		File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).
		File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{CreateDestPath: true},
			llb.WithUser("builder:staff"),
		),
	)

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "file.copy")
	require.Contains(t, unsupportedErr.Reason, "requires container context")
}

func TestDefinitionToIDFileMkdirNamedChownWithoutContainerContextUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().File(
		llb.Mkdir(
			"/dst",
			0o755,
			llb.WithParents(true),
			llb.WithUser("builder:staff"),
		),
	)

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "file.mkdir")
	require.Contains(t, unsupportedErr.Reason, "requires container context")
}

func TestDefinitionToIDFileMkdirNamedChownWithContainerContext(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").File(
		llb.Mkdir(
			"/dst",
			0o755,
			llb.WithParents(true),
			llb.WithUser("builder:staff"),
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, "container", fieldsFromRoot(id)[0])

	require.NotNil(t, findFieldInChain(id, "withRootfs"))

	withDir := findFieldInChain(id, "withDirectory")
	require.NotNil(t, withDir)
	require.Equal(t, "/dst", withDir.Arg("path").Value().ToInput())
	require.Equal(t, "builder:staff", withDir.Arg("owner").Value().ToInput())
	require.EqualValues(t, 0o755, withDir.Arg("permissions").Value().ToInput())

	sourceID, ok := withDir.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	sourceDir := findFieldInChain(sourceID, "directory")
	require.NotNil(t, sourceDir)
	require.Equal(t, mkdirCompatSyntheticSourcePath, sourceDir.Arg("path").Value().ToInput())
}

func TestDefinitionToIDFileCopyNamedChownWithContainerContext(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().
		File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).
		File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Image("alpine").File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{CreateDestPath: true},
			llb.WithUser("builder:staff"),
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)
	require.Equal(t, "container", fieldsFromRoot(id)[0])

	withFile := findFieldInChain(id, "withFile")
	require.NotNil(t, withFile)
	require.Equal(t, "/dst/a.txt", withFile.Arg("path").Value().ToInput())
	require.Equal(t, "builder:staff", withFile.Arg("owner").Value().ToInput())

	sourceID, ok := withFile.Arg("source").Value().ToInput().(*call.ID)
	require.True(t, ok)
	sourceFile := findFieldInChain(sourceID, "file")
	require.NotNil(t, sourceFile)
	require.Equal(t, "a.txt", sourceFile.Arg("path").Value().ToInput())
}

func TestDefinitionToIDFileCopyGroupOnlyChown(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{
				CreateDestPath: true,
				ChownOpt: &llb.ChownOpt{
					Group: &llb.UserOpt{UID: 789},
				},
			},
		),
	)

	id, err := DefinitionToID(marshalStateToPB(t, st), nil)
	require.NoError(t, err)

	withFile := rootfsArgFromContainer(t, id)
	require.Equal(t, []string{"directory", "withFile"}, fieldsFromRoot(withFile))
	require.Equal(t, "0:789", withFile.Arg("owner").Value().ToInput())
}

func TestDefinitionToIDFileCopyAlwaysReplaceUnsupported(t *testing.T) {
	t.Parallel()

	src := llb.Scratch().File(llb.Mkdir("/src", 0o755, llb.WithParents(true))).File(llb.Mkfile("/src/a.txt", 0o644, []byte("hello")))
	st := llb.Scratch().File(
		llb.Copy(
			src,
			"/src/a.txt",
			"/dst/a.txt",
			&llb.CopyInfo{
				CreateDestPath:                 true,
				AlwaysReplaceExistingDestPaths: true,
			},
		),
	)

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "file.copy")
	require.Contains(t, unsupportedErr.Reason, "alwaysReplaceExistingDestPaths")
}

func TestDefinitionToIDFileMkfileBinaryUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().File(llb.Mkfile("/bin.dat", 0o644, []byte{0xff, 0x00}))
	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "file.mkfile")
	require.Contains(t, unsupportedErr.Reason, "binary")
}

func TestDefinitionToIDUnsupportedSourceScheme(t *testing.T) {
	t.Parallel()

	op := &pb.Op{
		Op: &pb.Op_Source{
			Source: &pb.SourceOp{
				Identifier: "unknown-scheme://opaque",
			},
		},
	}

	_, err := DefinitionToID(singleOpDefinition(t, op), nil)
	unsupportedErr := requireUnsupported(t, err, "source")
	require.Contains(t, strings.ToLower(unsupportedErr.Reason), "unsupported source scheme")
}

func TestDefinitionToIDDeterministicIDEncoding(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(llb.Args([]string{"echo", "hello"})).Root()
	def := marshalStateToPB(t, st)

	idA, err := DefinitionToID(def, nil)
	require.NoError(t, err)
	idB, err := DefinitionToID(def, nil)
	require.NoError(t, err)

	encA, err := idA.Encode()
	require.NoError(t, err)
	encB, err := idB.Encode()
	require.NoError(t, err)

	require.Equal(t, idA.Digest(), idB.Digest())
	require.Equal(t, encA, encB)
}

func fieldsFromRoot(id *call.ID) []string {
	rev := []string{}
	for cur := id; cur != nil; cur = cur.Receiver() {
		rev = append(rev, cur.Field())
	}
	fields := make([]string, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		fields = append(fields, rev[i])
	}
	return fields
}

func findFieldInChain(id *call.ID, field string) *call.ID {
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == field {
			return cur
		}
	}
	return nil
}

func findFieldsInChain(id *call.ID, field string) []*call.ID {
	fields := []*call.ID{}
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == field {
			fields = append(fields, cur)
		}
	}
	return fields
}

func fieldIndices(fields []string, field string) []int {
	idxs := []int{}
	for i, f := range fields {
		if f == field {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

func requireUnsupported(t *testing.T, err error, opType string) *UnsupportedOpError {
	t.Helper()
	require.Error(t, err)
	var unsupportedErr *UnsupportedOpError
	require.ErrorAs(t, err, &unsupportedErr)
	require.Equal(t, opType, unsupportedErr.OpType)
	return unsupportedErr
}

func marshalStateToPB(t *testing.T, st llb.State) *pb.Definition {
	t.Helper()
	def, err := st.Marshal(context.Background())
	require.NoError(t, err)
	return def.ToPB()
}

func singleOpDefinition(t *testing.T, op *pb.Op) *pb.Definition {
	t.Helper()
	dt, err := op.Marshal()
	require.NoError(t, err)
	return &pb.Definition{Def: [][]byte{dt}}
}

func rootfsArgFromContainer(t *testing.T, id *call.ID) *call.ID {
	t.Helper()
	require.NotNil(t, id)
	withRootfs := id
	if withRootfs.Field() != "withRootfs" {
		withRootfs = findFieldInChain(id, "withRootfs")
		require.NotNil(t, withRootfs)
	}
	arg := withRootfs.Arg("directory")
	require.NotNil(t, arg)
	rootfsID, ok := arg.Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.NotNil(t, rootfsID)
	return rootfsID
}

func fakeSecretID(name string) *call.ID {
	return appendCall(call.New(), secretType(), "secret", argString("name", name))
}

func fakeSocketID(path string) *call.ID {
	host := appendCall(call.New(), hostType(), "host")
	return appendCall(host, socketType(), "unixSocket", argString("path", path))
}
