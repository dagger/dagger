package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaASTCacheScopes(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("schema-ast-memo")
	module := f.module(ctx, "fixture", moduleOwnershipModuleOpts{})
	mods := []modInstall{{mod: NewUserMod(module)}}
	first := schemaASTCache(mods)
	require.NotNil(t, first)
	require.Same(t, first, schemaASTCache(mods), "sessions installing the same modules share their description")
	require.Same(t, first, schemaASTCache([]modInstall{{mod: NewUserMod(module)}}))
	require.NotSame(t, first, schemaASTCache([]modInstall{{mod: NewUserMod(module), opts: InstallOpts{Main: true}}}))
	other := f.module(ctx, "fixture", moduleOwnershipModuleOpts{variant: "other"})
	require.NotSame(t, first, schemaASTCache([]modInstall{{mod: NewUserMod(other)}}))
	require.Nil(t, schemaASTCache([]modInstall{{mod: &memoTestMod{name: "dynamic"}}}))

	// Only metadata is shared across roots; the builder still owns a separate
	// registry for every root, as TestSchemaBuilderMemo also requires.
	memo := NewSchemaBuilderMemo()
	a := memo.Get(NewSchemaBuilder(&Query{}, []Mod{NewUserMod(module)}))
	b := memo.Get(NewSchemaBuilder(&Query{}, []Mod{NewUserMod(module)}))
	require.NotSame(t, a, b)

	// An environment expansion may produce different defaults for different
	// callers even when they use the same operational module result.
	module.Self().GetSource().UserDefaults = NewEnvFile(true).WithVariable("LABEL", "${HOST_LABEL}")
	require.Nil(t, schemaASTCache(mods))
}
