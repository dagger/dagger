package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestModuleEnumPrefersOwningModuleNames(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "enum-owner")
	ctx, cache, srv := env.open(t)
	_ = cache
	member := env.attach(t, ctx, cache, srv, "enum-member", NewEnumMemberTypeDef("Ready", "ready", "", nil, dagql.ObjectResult[*SourceMap]{})).(dagql.ObjectResult[*EnumMemberTypeDef])
	def := NewEnumTypeDef("Status", "", dagql.ObjectResult[*SourceMap]{})
	def.Members = dagql.ObjectResultArray[*EnumMemberTypeDef]{member}
	defRes := env.attach(t, ctx, cache, srv, "enum-def", def).(dagql.ObjectResult[*EnumTypeDef])
	typeRes := env.attach(t, ctx, cache, srv, "enum-typedef", (&TypeDef{}).WithEnum(defRes)).(dagql.ObjectResult[*TypeDef])
	srv.InstallScalar(&ModuleEnum{TypeDef: def})
	deps := &SchemaBuilder{clientOwned: true, lazilyLoadedServer: srv}
	mod := env.attach(t, ctx, cache, srv, "enum-module", &Module{NameField: "owner", Deps: deps, EnumDefs: dagql.ObjectResultArray[*TypeDef]{typeRes}}).(dagql.ObjectResult[*Module])
	typ := &ModuleEnumType{typeDef: def, mod: mod}
	decoder, err := typ.getEnum(ctx)
	require.NoError(t, err)
	require.True(t, decoder.Local, "the entrypoint schema may already contain this module's enum")
	decoded, err := decoder.DecodeInput("Ready")
	require.NoError(t, err)
	require.Equal(t, "READY", decoded.(*ModuleEnum).Name)
	input, err := typ.ConvertToSDKInput(ctx, &ModuleEnum{TypeDef: def, Name: "READY"})
	require.NoError(t, err)
	require.Equal(t, "Ready", input)
	other := env.attach(t, ctx, cache, srv, "other-module", &Module{NameField: "other", Deps: deps}).(dagql.ObjectResult[*Module])
	external := &ModuleEnumType{typeDef: def, mod: other}
	input, err = external.ConvertToSDKInput(ctx, &ModuleEnum{TypeDef: def, Name: "READY"})
	require.NoError(t, err)
	require.Equal(t, "READY", input, "a consuming module uses canonical names")
}
