package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaASTMemoScopes(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("schema-ast-memo")
	module := f.module(ctx, "fixture", moduleOwnershipModuleOpts{})
	mods := []modInstall{{mod: NewUserMod(module)}}
	memo := NewSchemaBuilderMemo()
	first := memo.schemaASTCache(mods)
	require.NotNil(t, first)
	require.Same(t, first, memo.schemaASTCache(mods))
	require.NotSame(t, first, NewSchemaBuilderMemo().schemaASTCache(mods))
	require.NotSame(t, first, memo.schemaASTCache([]modInstall{{mod: NewUserMod(module), opts: InstallOpts{Main: true}}}))
	other := f.module(ctx, "fixture", moduleOwnershipModuleOpts{variant: "other"})
	require.NotSame(t, first, memo.schemaASTCache([]modInstall{{mod: NewUserMod(other)}}))
	require.Nil(t, memo.schemaASTCache([]modInstall{{mod: &memoTestMod{name: "dynamic"}}}))

	// Only metadata is shared across roots; the builder still owns a separate
	// registry for every root, as TestSchemaBuilderMemo also requires.
	a := memo.Get(NewSchemaBuilder(&Query{}, []Mod{NewUserMod(module)}))
	b := memo.Get(NewSchemaBuilder(&Query{}, []Mod{NewUserMod(module)}))
	require.NotSame(t, a, b)

	// An environment expansion may produce different defaults for different
	// callers even when they use the same operational module result.
	module.Self().GetSource().UserDefaults = NewEnvFile(true).WithVariable("LABEL", "${HOST_LABEL}")
	require.Nil(t, memo.schemaASTCache(mods))
}
