package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// followsTestEngine is one cache with core's whole schema, as an engine has
// it.
type followsTestEngine struct {
	t   *testing.T
	ctx context.Context
	srv *dagql.Server
}

func newFollowsTestEngine(t *testing.T, salt []byte, session string) *followsTestEngine {
	t.Helper()
	e := newTransferTestEngine(t, salt)
	return &followsTestEngine{t: t, ctx: e.session(t, session), srv: e.dag}
}

func followsArg(name string, value dagql.Input) dagql.NamedInput {
	return dagql.NamedInput{Name: name, Value: value}
}

func (e *followsTestEngine) dir(from dagql.AnyObjectResult, field string, args ...dagql.NamedInput) dagql.ObjectResult[*core.Directory] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.Directory]
	require.NoError(e.t, e.srv.Select(e.ctx, from, &out, dagql.Selector{Field: field, Args: args}), field)
	return out
}

func (e *followsTestEngine) file(from dagql.AnyObjectResult, field string, args ...dagql.NamedInput) dagql.ObjectResult[*core.File] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.File]
	require.NoError(e.t, e.srv.Select(e.ctx, from, &out, dagql.Selector{Field: field, Args: args}), field)
	return out
}

func (e *followsTestEngine) changeset(from dagql.AnyObjectResult, field string, args ...dagql.NamedInput) dagql.ObjectResult[*core.Changeset] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.Changeset]
	require.NoError(e.t, e.srv.Select(e.ctx, from, &out, dagql.Selector{Field: field, Args: args}), field)
	return out
}

// value is the field's scalar or list result, encoded.
func (e *followsTestEngine) value(from dagql.AnyObjectResult, field string, args ...dagql.NamedInput) string {
	e.t.Helper()
	var out dagql.AnyResult
	require.NoError(e.t, e.srv.Select(e.ctx, from, &out, dagql.Selector{Field: field, Args: args}), field)
	raw, err := json.Marshal(out.Unwrap())
	require.NoError(e.t, err)
	return string(raw)
}

// digest is the content digest core gives a directory or file.
func (e *followsTestEngine) digest(res dagql.AnyObjectResult) string {
	e.t.Helper()
	var out dagql.String
	require.NoError(e.t, e.srv.Select(e.ctx, res, &out, dagql.Selector{Field: "digest"}))
	return out.String()
}

func (e *followsTestEngine) ctr(from dagql.AnyObjectResult, field string, args ...dagql.NamedInput) dagql.ObjectResult[*core.Container] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.Container]
	require.NoError(e.t, e.srv.Select(e.ctx, from, &out, dagql.Selector{Field: field, Args: args}), field)
	return out
}

// ctrState is what core gives a container: its rootfs's content digest, and
// its configuration as its own fields read it.
func (e *followsTestEngine) ctrState(c dagql.ObjectResult[*core.Container]) string {
	e.t.Helper()
	state := e.digest(e.dir(c, "rootfs"))
	for _, field := range []string{"entrypoint", "defaultArgs", "user", "workdir", "envVariables", "labels", "exposedPorts", "mounts"} {
		state += " " + field + "=" + e.value(c, field)
	}
	return state
}

func followsID[T dagql.Typed](t *testing.T, res dagql.ObjectResult[T]) dagql.ID[T] {
	t.Helper()
	id, err := res.ID()
	require.NoError(t, err)
	return dagql.NewID[T](id)
}

// followsResults runs every Directory, File, Changeset and Container field core
// declares to follow from its inputs (dagql.FieldSpec.FollowsFromInputs) on
// e, over inputs it makes itself, and returns what core's content digest
// gives for each result, or the result itself for a scalar.
func followsResults(t *testing.T, e *followsTestEngine) map[string]string {
	t.Helper()
	root := e.srv.Root()
	out := map[string]string{}
	scratch := e.dir(root, "directory")
	out["Query.directory"] = e.digest(scratch)
	base := e.dir(scratch, "withNewFile", followsArg("path", dagql.NewString("a.txt")), followsArg("contents", dagql.NewString("A")))
	base = e.dir(base, "withNewDirectory", followsArg("path", dagql.NewString("d")))
	base = e.dir(base, "withNewFile", followsArg("path", dagql.NewString("d/b.txt")), followsArg("contents", dagql.NewString("B")))
	// Enough paths that a field walking a map of them would write them in
	// another order on the second engine with no real chance of a match.
	var many []dagql.NamedInput
	for i := range 64 {
		many = append(many, followsArg("path", dagql.NewString(fmt.Sprintf("m/%d/f%02d.txt", i%8, i))))
	}
	for i, path := range many {
		base = e.dir(base, "withNewFile", path, followsArg("contents", dagql.NewString(fmt.Sprint(i))))
	}
	out["Directory.withNewFile, withNewDirectory"] = e.digest(base)
	a := e.file(base, "file", followsArg("path", dagql.NewString("a.txt")))
	out["Directory.file"] = e.digest(a)
	out["Directory.directory"] = e.digest(e.dir(base, "directory", followsArg("path", dagql.NewString("d"))))
	out["Directory.withDirectory"] = e.digest(e.dir(scratch, "withDirectory", followsArg("path", dagql.NewString("x")), followsArg("source", followsID(t, base))))
	out["Directory.withFile"] = e.digest(e.dir(scratch, "withFile", followsArg("path", dagql.NewString("f.txt")), followsArg("source", followsID(t, a))))
	out["Directory.withFiles"] = e.digest(e.dir(scratch, "withFiles", followsArg("path", dagql.NewString("p")), followsArg("sources", dagql.ArrayInput[dagql.ID[*core.File]]{followsID(t, a)})))
	out["Directory.withoutDirectory"] = e.digest(e.dir(base, "withoutDirectory", followsArg("path", dagql.NewString("d"))))
	out["Directory.withoutFile"] = e.digest(e.dir(base, "withoutFile", followsArg("path", dagql.NewString("a.txt"))))
	out["Directory.withoutFiles"] = e.digest(e.dir(base, "withoutFiles", followsArg("paths", dagql.ArrayInput[dagql.String]{dagql.NewString("a.txt")})))
	out["Directory.withSymlink"] = e.digest(e.dir(base, "withSymlink", followsArg("target", dagql.NewString("a.txt")), followsArg("linkName", dagql.NewString("l"))))
	out["Directory.withTimestamps"] = e.digest(e.dir(base, "withTimestamps", followsArg("timestamp", dagql.Int(1))))
	out["Directory.filter"] = e.digest(e.dir(base, "filter", followsArg("include", dagql.ArrayInput[dagql.String]{dagql.NewString("*.txt")})))
	smaller := e.dir(base, "withoutFile", followsArg("path", dagql.NewString("a.txt")))
	out["Directory.diff"] = e.digest(e.dir(base, "diff", followsArg("other", followsID(t, smaller))))
	changes := e.changeset(base, "changes", followsArg("from", followsID(t, smaller)))
	out["Directory.changes, withChanges"] = e.digest(e.dir(smaller, "withChanges", followsArg("changes", followsID(t, changes))))
	out["Changeset.filter, Directory.withChanges"] = e.digest(e.dir(smaller, "withChanges", followsArg("changes", followsID(t, e.changeset(changes, "filter", followsArg("include", dagql.ArrayInput[dagql.String]{dagql.NewString("*.txt")}))))))
	out["Directory.withPlatform"] = e.digest(e.dir(base, "withPlatform", followsArg("platform", core.Platform{OS: "linux", Architecture: "arm64"})))
	out["Directory.withPatch"] = e.digest(e.dir(base, "withPatch", followsArg("patch", dagql.NewString("--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-A\n\\ No newline at end of file\n+C\n\\ No newline at end of file\n"))))
	out["Directory.entries"] = e.value(base, "entries")
	out["Directory.glob"] = e.value(base, "glob", followsArg("pattern", dagql.NewString("**")))
	out["Directory.digest"] = e.digest(base)
	out["Directory.name"] = e.value(base, "name")
	out["Directory.stat"] = e.value(base, "stat", followsArg("path", dagql.NewString("a.txt")))
	made := e.file(root, "file", followsArg("name", dagql.NewString("q.txt")), followsArg("contents", dagql.NewString("Q")))
	out["Query.file"] = e.digest(made)
	out["Query.blob"] = e.digest(e.file(root, "blob", followsArg("name", dagql.NewString("q.bin")), followsArg("contents", dagql.Bytes("Q"))))
	out["File.contents"] = e.value(a, "contents")
	out["File.digest"] = e.digest(a)
	out["File.name"] = e.value(a, "name")
	out["File.size"] = e.value(a, "size")
	out["File.stat"] = e.value(a, "stat")
	out["File.withName"] = e.digest(e.file(a, "withName", followsArg("name", dagql.NewString("renamed.txt"))))
	out["File.withTimestamps"] = e.digest(e.file(a, "withTimestamps", followsArg("timestamp", dagql.Int(1))))

	str := dagql.NewString
	strs := func(v ...string) dagql.ArrayInput[dagql.String] {
		return dagql.ArrayInput[dagql.String](dagql.NewStringArray(v...))
	}
	c := e.ctr(root, "container")
	out["Query.container"] = e.ctrState(c)
	c = e.ctr(c, "withNewFile", followsArg("path", str("/n.txt")), followsArg("contents", str("N")))
	out["Container.withNewFile"] = e.ctrState(c)
	out["Container.withDirectory"] = e.ctrState(e.ctr(c, "withDirectory", followsArg("path", str("/d")), followsArg("source", followsID(t, base))))
	out["Container.withFile"] = e.ctrState(e.ctr(c, "withFile", followsArg("path", str("/f.txt")), followsArg("source", followsID(t, a))))
	out["Container.withFiles"] = e.ctrState(e.ctr(c, "withFiles", followsArg("path", str("/p")), followsArg("sources", dagql.ArrayInput[dagql.ID[*core.File]]{followsID(t, a)})))
	out["Container.withoutFile"] = e.ctrState(e.ctr(c, "withoutFile", followsArg("path", str("/n.txt"))))
	out["Container.withoutFiles"] = e.ctrState(e.ctr(c, "withoutFiles", followsArg("paths", strs("/n.txt"))))
	withD := e.ctr(c, "withDirectory", followsArg("path", str("/d")), followsArg("source", followsID(t, base)))
	out["Container.withoutDirectory"] = e.ctrState(e.ctr(withD, "withoutDirectory", followsArg("path", str("/d"))))
	out["Container.withSymlink"] = e.ctrState(e.ctr(c, "withSymlink", followsArg("target", str("/n.txt")), followsArg("linkName", str("/l"))))
	out["Container.withEnvVariable"] = e.ctrState(e.ctr(c, "withEnvVariable", followsArg("name", str("E")), followsArg("value", str("v"))))
	withE := e.ctr(c, "withEnvVariable", followsArg("name", str("E")), followsArg("value", str("v")))
	out["Container.withoutEnvVariable"] = e.ctrState(e.ctr(withE, "withoutEnvVariable", followsArg("name", str("E"))))
	out["Container.withWorkdir"] = e.ctrState(e.ctr(c, "withWorkdir", followsArg("path", str("/w"))))
	out["Container.withoutWorkdir"] = e.ctrState(e.ctr(c, "withoutWorkdir"))
	mounted := e.ctr(c, "withMountedDirectory", followsArg("path", str("/m")), followsArg("source", followsID(t, base)))
	out["Container.withMountedDirectory"] = e.ctrState(mounted) + " /m=" + e.digest(e.dir(mounted, "directory", followsArg("path", str("/m"))))
	mountedFile := e.ctr(c, "withMountedFile", followsArg("path", str("/mf.txt")), followsArg("source", followsID(t, a)))
	out["Container.withMountedFile"] = e.ctrState(mountedFile) + " /mf.txt=" + e.digest(e.file(mountedFile, "file", followsArg("path", str("/mf.txt"))))
	mountedFiles := e.ctr(c, "withMountedFiles", followsArg("path", str("/mfs")), followsArg("sources", dagql.ArrayInput[dagql.ID[*core.File]]{followsID(t, a)}))
	out["Container.withMountedFiles"] = e.ctrState(mountedFiles)
	var volume dagql.ObjectResult[*core.CacheVolume]
	require.NoError(t, e.srv.Select(e.ctx, root, &volume, dagql.Selector{Field: "cacheVolume", Args: []dagql.NamedInput{followsArg("key", str("follows"))}}))
	out["Container.withMountedCache"] = e.ctrState(e.ctr(c, "withMountedCache", followsArg("path", str("/cache")), followsArg("cache", followsID(t, volume))))
	out["Container.withMountedTemp"] = e.ctrState(e.ctr(c, "withMountedTemp", followsArg("path", str("/t"))))
	out["Container.withoutMount"] = e.ctrState(e.ctr(mounted, "withoutMount", followsArg("path", str("/m"))))
	secret := e.secret(root)
	out["Container.withMountedSecret"] = e.ctrState(e.ctr(c, "withMountedSecret", followsArg("path", str("/s")), followsArg("source", followsID(t, secret))))
	out["Container.withSecretVariable"] = e.ctrState(e.ctr(c, "withSecretVariable", followsArg("name", str("S")), followsArg("secret", followsID(t, secret))))
	withS := e.ctr(c, "withSecretVariable", followsArg("name", str("S")), followsArg("secret", followsID(t, secret)))
	out["Container.withoutSecretVariable"] = e.ctrState(e.ctr(withS, "withoutSecretVariable", followsArg("name", str("S"))))
	out["Container.withEntrypoint"] = e.ctrState(e.ctr(c, "withEntrypoint", followsArg("args", strs("/bin/e"))))
	withEP := e.ctr(c, "withEntrypoint", followsArg("args", strs("/bin/e")))
	out["Container.withoutEntrypoint"] = e.ctrState(e.ctr(withEP, "withoutEntrypoint"))
	out["Container.withDefaultArgs"] = e.ctrState(e.ctr(c, "withDefaultArgs", followsArg("args", strs("x"))))
	withDA := e.ctr(c, "withDefaultArgs", followsArg("args", strs("x")))
	out["Container.withoutDefaultArgs"] = e.ctrState(e.ctr(withDA, "withoutDefaultArgs"))
	out["Container.withLabel"] = e.ctrState(e.ctr(c, "withLabel", followsArg("name", str("l")), followsArg("value", str("v"))))
	withL := e.ctr(c, "withLabel", followsArg("name", str("l")), followsArg("value", str("v")))
	out["Container.withoutLabel"] = e.ctrState(e.ctr(withL, "withoutLabel", followsArg("name", str("l"))))
	out["Container.withUser"] = e.ctrState(e.ctr(c, "withUser", followsArg("name", str("u"))))
	out["Container.withoutUser"] = e.ctrState(e.ctr(e.ctr(c, "withUser", followsArg("name", str("u"))), "withoutUser"))
	out["Container.withExposedPort"] = e.ctrState(e.ctr(c, "withExposedPort", followsArg("port", dagql.NewInt(80))))
	out["Container.withoutExposedPort"] = e.ctrState(e.ctr(e.ctr(c, "withExposedPort", followsArg("port", dagql.NewInt(80))), "withoutExposedPort", followsArg("port", dagql.NewInt(80))))
	out["Container.withAnnotation"] = e.ctrState(e.ctr(c, "withAnnotation", followsArg("name", str("a")), followsArg("value", str("v"))))
	out["Container.withoutAnnotation"] = e.ctrState(e.ctr(e.ctr(c, "withAnnotation", followsArg("name", str("a")), followsArg("value", str("v"))), "withoutAnnotation", followsArg("name", str("a"))))
	out["Container.withRootfs"] = e.ctrState(e.ctr(c, "withRootfs", followsArg("directory", followsID(t, base))))
	service := e.service(root)
	out["Container.withServiceBinding"] = e.ctrState(e.ctr(c, "withServiceBinding", followsArg("alias", str("svc")), followsArg("service", followsID(t, service))))
	out["Container.directory"] = e.digest(e.dir(withD, "directory", followsArg("path", str("/d"))))
	out["Container.file"] = e.digest(e.file(c, "file", followsArg("path", str("/n.txt"))))
	out["Container.rootfs"] = e.digest(e.dir(c, "rootfs"))
	return out
}

func (e *followsTestEngine) secret(root dagql.AnyObjectResult) dagql.ObjectResult[*core.Secret] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.Secret]
	require.NoError(e.t, e.srv.Select(e.ctx, root, &out, dagql.Selector{Field: "setSecret", Args: []dagql.NamedInput{followsArg("name", dagql.NewString("s")), followsArg("plaintext", dagql.NewString("p"))}}))
	return out
}

func (e *followsTestEngine) service(root dagql.AnyObjectResult) dagql.ObjectResult[*core.Service] {
	e.t.Helper()
	var out dagql.ObjectResult[*core.Service]
	require.NoError(e.t, e.srv.Select(e.ctx, root, &out, dagql.Selector{Field: "container"}, dagql.Selector{Field: "asService", Args: []dagql.NamedInput{followsArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("true")))}}))
	return out
}

// A field that declares its result follows from its inputs meets the bar
// dagql.FieldSpec.FollowsFromInputs states: two runs of it over the same
// inputs give the same content digest. Every field core declares is run on
// two caches, a second or more apart, so any
// time a step writes would differ between them, and what core's content
// digest gives is compared. Some of them mount snapshots, as a diff does,
// which takes CAP_SYS_ADMIN, so it runs privileged, in test-split's testEbpf.
func TestDeclaredFieldsFollowFromTheirInputs(t *testing.T) {
	if err := gitMountNamespaces(); err != nil {
		t.Skipf("a diff mounts snapshots, which takes CAP_SYS_ADMIN; testEbpf runs this privileged: %v", err)
	}
	salt := transferTestSalt(t)
	first := followsResults(t, newFollowsTestEngine(t, salt, "follows-a"))
	time.Sleep(1100 * time.Millisecond)
	second := followsResults(t, newFollowsTestEngine(t, salt, "follows-b"))
	for name, value := range first {
		require.Equal(t, value, second[name], name)
	}
	require.Len(t, second, len(first))
}

// The fields that place a value without running a process, and read from
// outside the engine only content a hash in their key addresses, say so in
// every call core makes of them. Mounting a cache volume follows from its
// inputs, but a process run over that mount reads the volume's contents,
// which no call holds, so it doesn't. Inheriting a system variable only names
// it; the process that reads the engine's value doesn't follow either. A git
// tree's call says so unless it keeps .git
// (TestGitTreeFollowsFromItsInputsWithoutGitDir).
func TestFieldsThatFollowFromInputsDeclareIt(t *testing.T) {
	e := newFollowsTestEngine(t, transferTestSalt(t), "follows-declared")
	for _, tc := range []struct {
		typ, field string
		follows    bool
	}{
		{"Container", "from", true},
		{"Directory", "withPlatform", true},
		{"Container", "withMountedCache", true},
		{"Container", "__withSystemEnvVariable", true},
		{"GitRepository", "__sparseFiles", true},
		{"GitRef", "__sparseWorkspaceTree", true},
		{"Query", "__httpFile", true},
		{"Container", "withExec", false},
	} {
		typ, ok := e.srv.ObjectType(tc.typ)
		require.True(t, ok, tc.typ)
		spec, ok := typ.FieldSpec(tc.field, "")
		require.True(t, ok, tc.typ+"."+tc.field)
		require.Equal(t, tc.follows, spec.FollowsFromInputs, tc.typ+"."+tc.field)
	}

	root := e.srv.Root()
	base := e.dir(e.dir(root, "directory"), "withNewFile", followsArg("path", dagql.NewString("a.txt")), followsArg("contents", dagql.NewString("A")))
	var volume dagql.ObjectResult[*core.CacheVolume]
	require.NoError(t, e.srv.Select(e.ctx, root, &volume, dagql.Selector{Field: "cacheVolume", Args: []dagql.NamedInput{followsArg("key", dagql.NewString("declared"))}}))
	for name, res := range map[string]dagql.AnyObjectResult{
		"Directory.withPlatform":            e.dir(base, "withPlatform", followsArg("platform", core.Platform{OS: "linux", Architecture: "arm64"})),
		"Container.withMountedCache":        e.ctr(e.ctr(root, "container"), "withMountedCache", followsArg("path", dagql.NewString("/cache")), followsArg("cache", followsID(t, volume))),
		"Container.__withSystemEnvVariable": e.ctr(e.ctr(root, "container"), "__withSystemEnvVariable", followsArg("name", dagql.NewString("GODEBUG"))),
	} {
		frame, err := res.ResultCall()
		require.NoError(t, err, name)
		require.True(t, frame.FollowsFromInputs, name)
	}
}

// A git tree without .git is its commit's files, which its key's SHA
// addresses, so its call says it follows from its inputs. One that keeps
// .git doesn't (gitTreeFollowsFromInputs): its pack's bytes differ from run
// to run, and its tags, if it fetches them, are those the repository has
// then.
func TestGitTreeFollowsFromItsInputsWithoutGitDir(t *testing.T) {
	e := newFollowsTestEngine(t, transferTestSalt(t), "follows-tree")
	for _, typ := range []string{"GitRef", "GitCommit"} {
		class, ok := e.srv.ObjectType(typ)
		require.True(t, ok, typ)
		spec, ok := class.FieldSpec("tree", "")
		require.True(t, ok, typ)
		require.True(t, spec.FollowsFromInputs, typ+".tree")
	}

	ctx, srv, cache, _ := resolverOutputFixture(t)
	srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
	s := &gitSchema{}
	dagql.Fields[*core.GitRepository]{dagql.NodeFunc("ref", s.ref).View(AllVersion).IsPersistable()}.Install(srv)
	dagql.Fields[*core.GitRef]{dagql.NodeFuncWithDynamicInputs("tree", s.tree, s.treeCacheKey).FollowsFromInputs().View(AllVersion).IsPersistable()}.Install(srv)
	u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
	require.NoError(t, err)
	repo := resolverAttach(t, ctx, srv, cache, "repository", &core.GitRepository{Backend: &core.RemoteGitRepository{URL: u}, URL: dagql.NonNull(dagql.String(u.Remote()))})
	var ref dagql.ObjectResult[*core.GitRef]
	require.NoError(t, srv.Select(ctx, repo, &ref, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(strings.Repeat("a", 40))}}}))
	for _, tc := range []struct {
		name    string
		args    []dagql.NamedInput
		follows bool
	}{
		{"without .git", []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}, true},
		{"without .git, tags asked", []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}, {Name: "includeTags", Value: dagql.Boolean(true)}}, true},
		{"with .git", nil, false},
		{"with .git and tags", []dagql.NamedInput{{Name: "includeTags", Value: dagql.Boolean(true)}}, false},
	} {
		var tree dagql.ObjectResult[*core.Directory]
		require.NoError(t, srv.Select(ctx, ref, &tree, dagql.Selector{Field: "tree", Args: tc.args}), tc.name)
		frame, err := tree.ResultCall()
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.follows, frame.FollowsFromInputs, tc.name)
	}
}
