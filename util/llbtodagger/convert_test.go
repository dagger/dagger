package llbtodagger

import (
	"context"
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

func TestDefinitionToIDLocalFollowPathsUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Local("ctx", llb.FollowPaths([]string{"foo"}))
	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "source(local)")
	require.Contains(t, unsupportedErr.Reason, "follow paths")
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
	require.GreaterOrEqual(t, len(fields), 5)
	require.Equal(t, "container", fields[0])
	require.Equal(t, "withExec", fields[len(fields)-1])

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
}

func TestDefinitionToIDExecReadonlyBindUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddMount("/ro", llb.Scratch(), llb.Readonly),
	).Root()

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "readonly bind")
}

func TestDefinitionToIDExecNetworkUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Network(llb.NetModeHost).Run(llb.Shlex("echo hello")).Root()
	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "network mode")
}

func TestDefinitionToIDExecSecretUnsupported(t *testing.T) {
	t.Parallel()

	st := llb.Image("alpine").Run(
		llb.Shlex("echo hello"),
		llb.AddSecret("/run/secrets/token"),
	).Root()

	_, err := DefinitionToID(marshalStateToPB(t, st), nil)
	unsupportedErr := requireUnsupported(t, err, "exec")
	require.Contains(t, unsupportedErr.Reason, "secret")
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
