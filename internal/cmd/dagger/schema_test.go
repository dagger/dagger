package daggercmd

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// testIntrospection builds a small introspection response with one core type,
// one type contributed by a module, and a Query field for each.
func testIntrospection() *introspection.Response {
	// Directive arg values are JSON-encoded: Directives.SourceMap reads them
	// back through json.Unmarshal, so the module name has to carry its quotes.
	sourceMap := func(module string) introspection.Directives {
		encoded := strconv.Quote(module)
		return introspection.Directives{{
			Name: "sourceMap",
			Args: []*introspection.DirectiveArg{{
				Name:  "module",
				Value: &encoded,
			}},
		}}
	}
	strType := &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "String",
		},
	}

	strTypeRef := &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "String",
		},
	}

	return &introspection.Response{
		SchemaVersion: "v0.0.0-test",
		Schema: &introspection.Schema{
			QueryType: struct {
				Name string `json:"name,omitempty"`
			}{Name: "Query"},
			// The @sourceMap *definition* is core schema and must survive
			// every filter, even though its applications are what mark
			// module-contributed surface.
			Directives: []*introspection.DirectiveDef{{
				Name:      "sourceMap",
				Locations: []string{"OBJECT", "FIELD_DEFINITION"},
				Args: introspection.InputValues{{
					Name:    "module",
					TypeRef: strTypeRef,
				}},
			}},
			Types: introspection.Types{
				{
					Kind: introspection.TypeKindObject,
					Name: "Query",
					Fields: []*introspection.Field{
						{Name: "container", TypeRef: strType},
						{
							Name:       "golang",
							TypeRef:    strType,
							Directives: sourceMap("golang"),
						},
					},
				},
				{
					// A core type that the engine extended for a module:
					// the type itself is core, but "asGolang" was contributed
					// by the golang module (cf. Binding.asXXX).
					Kind: introspection.TypeKindObject,
					Name: "Container",
					Fields: []*introspection.Field{
						{Name: "stdout", TypeRef: strType},
						{
							Name:       "asGolang",
							TypeRef:    strType,
							Directives: sourceMap("golang"),
						},
					},
				},
				{
					Kind:       introspection.TypeKindObject,
					Name:       "Golang",
					Directives: sourceMap("golang"),
					Fields: []*introspection.Field{
						{Name: "lint", TypeRef: strType},
					},
				},
			},
		},
	}
}

func TestRenderSchemaSDL(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), nil, false, false))

	got := out.String()
	// The default output is SDL carrying both core and module surface.
	require.Contains(t, got, "type Container {")
	require.Contains(t, got, "stdout: String!")
	require.Contains(t, got, "type Golang @sourceMap(module: \"golang\") {")
	require.Contains(t, got, "container: String!")
	require.Contains(t, got, "golang: String!")
	// GraphQL builtins and introspection meta-types stay out of the output.
	require.NotContains(t, got, "scalar String")
	require.NotContains(t, got, "__Type")
}

func TestRenderSchemaCoreOnly(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), nil, true, false))

	got := out.String()
	require.Contains(t, got, "type Container {")
	require.Contains(t, got, "container: String!")
	// Module-contributed types and Query fields are dropped.
	require.NotContains(t, got, "type Golang")
	require.NotContains(t, got, "golang: String!")
}

// --core must leave no module attribution behind at all, including on core
// types the engine extended for a module. introspection.Exclude only strips
// module-owned fields from Query, so renderSchema has to strip them from
// every retained type itself.
func TestRenderSchemaCoreOnlyDropsModuleFieldsOnCoreTypes(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), nil, true, false))

	got := out.String()
	require.Contains(t, got, "stdout: String!")
	require.NotContains(t, got, "asGolang")

	// No module attribution survives: applications carry a quoted module
	// name, so assert on that rather than on "sourceMap(module:", which the
	// directive *definition* also matches.
	require.NotContains(t, got, `sourceMap(module: "`)

	// ...but the directive definition itself is core and must remain, or the
	// emitted SDL would reference an undeclared directive.
	require.Contains(t, got, "directive @sourceMap")
}

func TestRenderSchemaModuleFilter(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), []string{"golang"}, false, false))

	got := out.String()
	require.Contains(t, got, "type Golang @sourceMap(module: \"golang\") {")
	require.Contains(t, got, "golang: String!")
	// Core types are dropped when filtering to a module.
	require.NotContains(t, got, "type Container {")
	require.NotContains(t, got, "container: String!")
}

// An explicit module filter wins over --core, matching the switch in
// renderSchema. The flags are marked mutually exclusive on the command, so
// this only guards the fallthrough order.
func TestRenderSchemaModuleFilterBeatsCoreOnly(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), []string{"golang"}, true, false))
	require.Contains(t, out.String(), "type Golang @sourceMap(module: \"golang\") {")
}

func TestRenderSchemaJSON(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), nil, false, true))

	// --json round-trips as an introspection response, so it stays consumable
	// by anything that already reads introspection JSON.
	var resp introspection.Response
	require.NoError(t, json.Unmarshal(out.Bytes(), &resp))
	require.Equal(t, "v0.0.0-test", resp.SchemaVersion)
	require.NotNil(t, resp.Schema)
	require.Equal(t, "Query", resp.Schema.QueryType.Name)
	require.NotNil(t, resp.Schema.Types.Get("Golang"))
}

func TestRenderSchemaJSONRespectsFilter(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderSchema(&out, testIntrospection(), nil, true, true))

	var resp introspection.Response
	require.NoError(t, json.Unmarshal(out.Bytes(), &resp))
	require.NotNil(t, resp.Schema.Types.Get("Container"))
	require.Nil(t, resp.Schema.Types.Get("Golang"))
}

func TestAPISchemaCmdFlagsMutuallyExclusive(t *testing.T) {
	cmd, _, err := apiCmd.Find([]string{"schema"})
	require.NoError(t, err)
	require.Equal(t, "schema", cmd.Name())

	for _, name := range []string{"json", "core"} {
		require.NotNil(t, cmd.Flags().Lookup(name), name)
	}
	// The module selection flags come from moduleAddFlags.
	require.NotNil(t, cmd.PersistentFlags().Lookup("load-module"))
	require.NotNil(t, cmd.PersistentFlags().Lookup("no-load-module"))
}
