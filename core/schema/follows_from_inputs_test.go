package schema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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
	strs := func(v ...string) dagql.ArrayInput[dagql.String] { return dagql.ArrayInput[dagql.String](dagql.NewStringArray(v...)) }
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
// digest gives is compared.
func TestDeclaredFieldsFollowFromTheirInputs(t *testing.T) {
	salt := transferTestSalt(t)
	first := followsResults(t, newFollowsTestEngine(t, salt, "follows-a"))
	time.Sleep(1100 * time.Millisecond)
	second := followsResults(t, newFollowsTestEngine(t, salt, "follows-b"))
	for name, value := range first {
		require.Equal(t, value, second[name], name)
	}
	require.Len(t, second, len(first))
}
