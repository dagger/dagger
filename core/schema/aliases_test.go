package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type aliasProbe struct {
	Name string `field:"true"`
}

func (*aliasProbe) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasProbe", NonNull: true}
}

type aliasProbeArch string

var aliasProbeArchs = dagql.NewEnum[aliasProbeArch]()

var aliasProbeArchARM64 = aliasProbeArchs.WithAliases(aliasProbeArchs.Register("ARM64"), core.NameAliases("ARM_64"))

func (aliasProbeArch) Decoder() dagql.InputDecoder {
	return aliasProbeArchs
}

func (a aliasProbeArch) ToLiteral() call.Literal {
	return aliasProbeArchs.Literal(a)
}

func (aliasProbeArch) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasProbeArch", NonNull: true}
}

// newAliasSchemaTestServer serves Query.aliasProbe(legacyJSON: Boolean):
// AliasProbe with transition aliases: Query.oldAliasProbe, legacyJson and the
// type AliasProbeOld; and Query.aliasProbeArch(arch: AliasProbeArch!):
// AliasProbeArch!, whose ARM64 value has the alias ARM_64.
func newAliasSchemaTestServer(t *testing.T) (context.Context, *dagql.Server) {
	t.Helper()
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*aliasProbe]{
		Aliases: core.NameAliases("AliasProbeOld"),
	}))
	aliasProbeArchs.Install(dag)
	dagql.Fields[*core.Query]{
		dagql.Func("aliasProbe", func(_ context.Context, _ *core.Query, args struct {
			LegacyJSON bool `name:"legacyJSON" default:"false"`
		}) (*aliasProbe, error) {
			if args.LegacyJSON {
				return &aliasProbe{Name: "json"}, nil
			}
			return &aliasProbe{Name: "plain"}, nil
		}).
			WithAliases(core.NameAliases("oldAliasProbe")).
			Args(dagql.Arg("legacyJSON").WithAliases(core.NameAliases("legacyJson"))),
		dagql.Func("aliasProbeArch", func(_ context.Context, _ *core.Query, args struct {
			Arch aliasProbeArch
		}) (aliasProbeArch, error) {
			return args.Arch, nil
		}),
	}.Install(dag)
	dagql.Fields[*aliasProbe]{}.Install(dag)
	return ctx, dag
}

func aliasSchemaField(t *testing.T, schema *codegenintrospection.Schema, typeName, fieldName string) *codegenintrospection.Field {
	t.Helper()
	typ := schema.Types.Get(typeName)
	require.NotNil(t, typ, "type %s", typeName)
	return schemaField(typ, fieldName)
}

func schemaArg(field *codegenintrospection.Field, name string) *codegenintrospection.InputValue {
	for _, arg := range field.Args {
		if arg.Name == name {
			return &arg
		}
	}
	return nil
}

func roundTripJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

// The schema JSON SDKs generate code from never has the transition aliases;
// the one the in-engine Dang runtime typechecks against has the field and
// argument aliases, deprecated, but no type alias.
func TestSchemaJSONAliases(t *testing.T) {
	ctx, dag := newAliasSchemaTestServer(t)

	data, err := getSchemaJSON(nil, nil, dag.View, dag)
	require.NoError(t, err)
	plain := decodeSchemaResponse(t, data).Schema
	require.Nil(t, aliasSchemaField(t, plain, "Query", "oldAliasProbe"))
	probe := aliasSchemaField(t, plain, "Query", "aliasProbe")
	require.NotNil(t, probe)
	require.Nil(t, schemaArg(probe, "legacyJson"))
	require.NotNil(t, schemaArg(probe, "legacyJSON"))
	require.Nil(t, plain.Types.Get("AliasProbeOld"))

	data, err = buildSchemaJSON(nil, nil, dag.View, dag, true)
	require.NoError(t, err)
	aliased := decodeSchemaResponse(t, data).Schema
	old := aliasSchemaField(t, aliased, "Query", "oldAliasProbe")
	require.NotNil(t, old)
	require.True(t, old.IsDeprecated)
	require.Equal(t, "Use `aliasProbe` instead.", *old.DeprecationReason)
	for _, field := range []*codegenintrospection.Field{old, aliasSchemaField(t, aliased, "Query", "aliasProbe")} {
		arg := schemaArg(field, "legacyJson")
		require.NotNil(t, arg)
		require.True(t, arg.IsDeprecated)
		require.NotNil(t, schemaArg(field, "legacyJSON"))
	}
	require.Nil(t, aliased.Types.Get("AliasProbeOld"))
	require.NotNil(t, aliased.Types.Get("AliasProbe"))

	// Enum value aliases are in it too, so a Dang caller passing an old
	// value typechecks; the plain JSON doesn't have them.
	enumValues := func(schema *codegenintrospection.Schema) map[string]*string {
		typ := schema.Types.Get("AliasProbeArch")
		require.NotNil(t, typ)
		values := map[string]*string{}
		for _, value := range typ.EnumValues {
			values[value.Name] = value.DeprecationReason
		}
		return values
	}
	require.Equal(t, map[string]*string{"ARM64": nil}, enumValues(plain))
	reason := "Use `ARM64` instead."
	require.Equal(t, map[string]*string{"ARM64": nil, "ARM_64": &reason}, enumValues(aliased))

	// __schemaJSONFile only includes them when the engine asks for them.
	_, err = dag.Query(ctx, `{ __schemaJSONFile(includeAliases: true) { id } }`, nil)
	require.ErrorContains(t, err, "internal argument")
}

// Queries may use the aliases in the views that have the new names.
func TestNameAliasesQuery(t *testing.T) {
	ctx, dag := newAliasSchemaTestServer(t)
	res, err := dag.Query(ctx, `{
		oldAliasProbe(legacyJson: true) { ... on AliasProbeOld { name } }
		aliasProbe { name }
	}`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"oldAliasProbe": map[string]any{"name": "json"},
		"aliasProbe":    map[string]any{"name": "plain"},
	}, roundTripJSON(t, res))

	// Enum value aliases decode to the canonical value.
	res, err = dag.Query(ctx, `{ old: aliasProbeArch(arch: ARM_64) canonical: aliasProbeArch(arch: ARM64) }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"old": string(aliasProbeArchARM64), "canonical": "ARM64"}, roundTripJSON(t, res))
}
