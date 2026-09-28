package schema

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// Loading a handle (node(id:), interface loads) recovers the schema its value
// belongs to through Query.ModDepsForCall. Recovering it again for the same
// module set must reuse the server the client already built rather than
// reinstall every module, while holding no cache rows of its own.
func TestModDepsForCallSharesSchemaPerModuleSet(t *testing.T) {
	f := newModuleOwnershipScopedTest(t)
	server, ok := f.root.Server.(*moduleOwnershipSchemaServer)
	require.True(t, ok)
	server.memo = core.NewSchemaBuilderMemo()
	parentCtx, installCtx, callCtx := f.session("parent"), f.session("install"), f.session("call")

	frameFor := func(name string) *dagql.ResultCall {
		t.Helper()
		mod := f.parent(parentCtx, moduleOwnershipParentOpts{name: name, definitions: true})
		f.install(installCtx, f.dag, mod)
		frame, err := selectObject(t, callCtx, f.dag, name).ResultCall()
		require.NoError(t, err)
		return frame
	}
	schemaFor := func(frame *dagql.ResultCall) *dagql.Server {
		t.Helper()
		deps, err := f.root.ModDepsForCall(callCtx, frame)
		require.NoError(t, err)
		srv, err := deps.Schema(callCtx)
		require.NoError(t, err)
		return srv
	}

	codegen := frameFor("codegen")
	first := schemaFor(codegen)
	_, ok = first.Root().ObjectType().FieldSpec("codegen", "")
	require.True(t, ok, "the recovered schema installs the value's module")
	require.Same(t, first, schemaFor(codegen), "the same module set reuses the built schema")

	other := schemaFor(frameFor("other"))
	require.NotSame(t, first, other, "another module set builds its own schema")
	_, ok = other.Root().ObjectType().FieldSpec("other", "")
	require.True(t, ok)
	require.Same(t, first, schemaFor(codegen), "memoizing another set keeps the first")

	server.memo = nil
	require.NotSame(t, first, schemaFor(codegen), "without a client memo every load builds")

	f.release(installCtx)
	f.release(parentCtx)
	f.release(callCtx)
	f.assertReleased("memoized schemas hold no cache rows")
}
