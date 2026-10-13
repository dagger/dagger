package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
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

// A module enum named by the new rules accepts the names the legacy rules
// gave its members as hidden aliases, decoded to the canonical member: same
// literal, so same IDs and cache keys.
func TestModuleEnumLegacyMemberAliases(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "enum-aliases")
	ctx, cache, srv := env.open(t)
	members := dagql.ObjectResultArray[*EnumMemberTypeDef]{}
	for _, name := range []string{"arm64", "IPv6", "GitHub", "E2ETest", "Ready"} {
		member := LatestNamer.NewEnumMemberTypeDef(name, "", "", nil, dagql.ObjectResult[*SourceMap]{})
		members = append(members, env.attach(t, ctx, cache, srv, "enum-member-"+name, member).(dagql.ObjectResult[*EnumMemberTypeDef]))
	}
	def := NewEnumTypeDef("Arch", "", dagql.ObjectResult[*SourceMap]{})
	def.Members = members

	enum := &ModuleEnum{TypeDef: def}
	for alias, canonical := range map[string]string{
		"ARM_64":     "ARM64",
		"I_PV_6":     "IPV6",
		"GIT_HUB":    "GITHUB",
		"E_2_E_TEST": "E2E_TEST",
		"ARM64":      "ARM64",
		"READY":      "READY",
	} {
		decoded, err := enum.DecodeInput(alias)
		require.NoError(t, err, alias)
		require.Equal(t, canonical, decoded.(*ModuleEnum).Name, alias)
		require.Equal(t, call.NewLiteralEnum(canonical), decoded.ToLiteral(), alias)
		out, err := json.Marshal(decoded)
		require.NoError(t, err)
		require.JSONEq(t, `"`+canonical+`"`, string(out), "outputs have the canonical name")
	}
	_, err := enum.DecodeInput("ARM_65")
	require.ErrorContains(t, err, `invalid enum member "ARM_65"`)

	// The declaring module decodes its original names, with no aliases.
	local := &ModuleEnum{TypeDef: def, Local: true}
	decoded, err := local.DecodeInput("arm64")
	require.NoError(t, err)
	require.Equal(t, "ARM64", decoded.(*ModuleEnum).Name)
	_, err = local.DecodeInput("ARM_64")
	require.Error(t, err)
	require.Empty(t, local.EnumValueAliases().Aliases)

	// The aliases are hidden: only the schema queries are validated against
	// has them, deprecated.
	srv.InstallScalar(enum)
	values := func(def *ast.Definition) []string {
		var names []string
		for _, value := range def.EnumValues {
			names = append(names, value.Name)
		}
		return names
	}
	require.Equal(t, []string{"ARM64", "IPV6", "GITHUB", "E2E_TEST", "READY"}, values(srv.Schema().Types["Arch"]))
	aliased := srv.SchemaWithAliases().Types["Arch"]
	require.Equal(t, []string{"ARM64", "ARM_64", "IPV6", "I_PV_6", "GITHUB", "GIT_HUB", "E2E_TEST", "E_2_E_TEST", "READY"}, values(aliased))
	dep := aliased.EnumValues.ForName("GIT_HUB").Directives.ForName("deprecated")
	require.NotNil(t, dep)
	require.Equal(t, "Use `GITHUB` instead.", dep.Arguments.ForName("reason").Value.Raw)
}

// A legacy module's enum members have no aliases.
func TestModuleEnumLegacyModuleHasNoAliases(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "enum-legacy")
	ctx, cache, srv := env.open(t)
	member := LegacyNamer.NewEnumMemberTypeDef("arm64", "", "", nil, dagql.ObjectResult[*SourceMap]{})
	require.Equal(t, "ARM_64", member.Name)
	def := NewEnumTypeDef("Arch", "", dagql.ObjectResult[*SourceMap]{})
	def.Members = dagql.ObjectResultArray[*EnumMemberTypeDef]{
		env.attach(t, ctx, cache, srv, "enum-member", member).(dagql.ObjectResult[*EnumMemberTypeDef]),
	}
	enum := &ModuleEnum{TypeDef: def}
	require.Empty(t, enum.EnumValueAliases().Aliases)
	_, err := enum.DecodeInput("ARM64")
	require.Error(t, err)
}

// An enum member's legacy name survives persistence.
func TestEnumMemberLegacyNamePersisted(t *testing.T) {
	member := LatestNamer.NewEnumMemberTypeDef("arm64", "", "", nil, dagql.ObjectResult[*SourceMap]{})
	require.Equal(t, "ARM_64", member.LegacyName)
	payload, err := encodePersistedEnumMemberTypeDef(nil, member)
	require.NoError(t, err)
	decoded, err := decodePersistedEnumMemberTypeDef(t.Context(), nil, payload)
	require.NoError(t, err)
	require.Equal(t, member, decoded)
}
