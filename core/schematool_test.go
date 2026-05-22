package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
)

// baseSchemaJSON is a minimal introspection schema with a Query root and one
// pre-existing type, used as the merge target in these tests.
const baseSchemaJSON = `{
  "__schema": {
    "queryType": {"name": "Query"},
    "types": [
      {"kind":"OBJECT","name":"Query","fields":[],"interfaces":[],"directives":[]},
      {"kind":"OBJECT","name":"Container","fields":[],"interfaces":[],"directives":[]}
    ],
    "directives": []
  },
  "__schemaVersion": "test"
}`

// echoModuleJSON declares an object but no Query type, so Merge must
// synthesize a no-arg constructor.
const echoModuleJSON = `{
  "__schema": {
    "types": [
      {"kind":"OBJECT","name":"Echo","description":"Echo object","fields":[
        {"name":"say","description":"Say something",
         "type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}},
         "args":[],"directives":[]}
      ],"interfaces":[],"directives":[]}
    ],
    "directives": []
  }
}`

// greeterModuleJSON declares its own Query type carrying the constructor
// field, so Merge must reuse that field (with its arguments).
const greeterModuleJSON = `{
  "__schema": {
    "queryType": {"name": "Query"},
    "types": [
      {"kind":"OBJECT","name":"Greeter","description":"Greeter object","fields":[],"interfaces":[],"directives":[]},
      {"kind":"OBJECT","name":"Query","fields":[
        {"name":"greeter","description":"Make a greeter",
         "type":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"Greeter"}},
         "args":[{"name":"prefix","description":"",
                  "type":{"kind":"SCALAR","name":"String"},"directives":[]}],
         "directives":[]}
      ],"interfaces":[],"directives":[]}
    ],
    "directives": []
  }
}`

const conflictModuleJSON = `{
  "__schema": {
    "types": [
      {"kind":"OBJECT","name":"Container","description":"my own container","fields":[],"interfaces":[],"directives":[]}
    ],
    "directives": []
  }
}`

const zooModuleJSON = `{
  "__schema": {
    "types": [
      {"kind":"INTERFACE","name":"Animal","description":"An animal","fields":[
        {"name":"sound","description":"Make a sound",
         "type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}},
         "args":[],"directives":[]}
      ],"interfaces":[],"directives":[]}
    ],
    "directives": []
  }
}`

const workflowModuleJSON = `{
  "__schema": {
    "types": [
      {"kind":"ENUM","name":"Status","description":"A state","enumValues":[
        {"name":"PENDING","description":"pending"},
        {"name":"ACTIVE","description":"active"}
      ],"interfaces":[],"directives":[]}
    ],
    "directives": []
  }
}`

func mustSchema(t *testing.T, jsonStr string) *Schema {
	t.Helper()
	s, err := NewSchema(JSON(jsonStr))
	require.NoError(t, err)
	return s
}

// The Schema type only exposes merge/contents, so these helpers read the
// parsed introspection directly to assert on a merged schema's contents.

func schemaType(s *Schema, name string) *codegenintrospection.Type {
	return s.Introspection.Schema.Types.Get(name)
}

func schemaHas(s *Schema, name string) bool {
	return schemaType(s, name) != nil
}

func schemaTypeNames(s *Schema, kind string) []string {
	out := []string{}
	for _, t := range s.Introspection.Schema.Types {
		if kind != "" && string(t.Kind) != kind {
			continue
		}
		out = append(out, t.Name)
	}
	return out
}

func TestNewSchema(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		s, err := NewSchema(JSON(baseSchemaJSON))
		require.NoError(t, err)
		require.True(t, schemaHas(s, "Query"))
	})
	t.Run("malformed JSON", func(t *testing.T) {
		_, err := NewSchema(JSON(`{not json`))
		require.Error(t, err)
	})
	t.Run("missing __schema", func(t *testing.T) {
		_, err := NewSchema(JSON(`{}`))
		require.ErrorContains(t, err, "__schema")
	})
}

func TestSchemaMerge(t *testing.T) {
	base := mustSchema(t, baseSchemaJSON)
	merged, err := base.Merge(JSON(echoModuleJSON), "echo")
	require.NoError(t, err)

	// The module's type is added and stamped with @sourceModuleName.
	echo := schemaType(merged, "Echo")
	require.NotNil(t, echo)
	stamp := echo.Directives.Directive(sourceModuleDirectiveName)
	require.NotNil(t, stamp)
	require.Equal(t, `"echo"`, *stamp.Arg("name"))

	// @sourceMap is also stamped so the codegen file-splitter
	// (DependencyNames/Include/Exclude) can place the type in
	// internal/dagger/echo.gen.go.
	echoSM := echo.Directives.SourceMap()
	require.NotNil(t, echoSM, "@sourceMap directive must be present on merged type")
	require.Equal(t, "echo", echoSM.Module)

	// A no-arg constructor pointing at the main object is synthesized.
	ctor := findField(schemaType(merged, "Query"), "echo")
	require.NotNil(t, ctor)
	require.Equal(t, codegenintrospection.TypeKindNonNull, ctor.TypeRef.Kind)
	require.Equal(t, "Echo", ctor.TypeRef.OfType.Name)
	require.Empty(t, ctor.Args)
	require.NotNil(t, ctor.Directives.Directive(sourceModuleDirectiveName))

	// The synthesized constructor field also carries @sourceMap.
	ctorSM := ctor.Directives.SourceMap()
	require.NotNil(t, ctorSM, "@sourceMap directive must be present on synthesized constructor field")
	require.Equal(t, "echo", ctorSM.Module)

	// The receiver is never mutated.
	require.False(t, schemaHas(base, "Echo"))
	require.Nil(t, findField(schemaType(base, "Query"), "echo"))
}

func TestSchemaMergeReusesModuleConstructor(t *testing.T) {
	merged, err := mustSchema(t, baseSchemaJSON).Merge(JSON(greeterModuleJSON), "greeter")
	require.NoError(t, err)

	require.True(t, schemaHas(merged, "Greeter"))
	// The module's Query type is not merged as a module-defined type.
	require.Equal(t, []string{"Query", "Container", "Greeter"}, schemaTypeNames(merged, "OBJECT"))

	// The constructor field declared by the module is reused, arguments
	// and all.
	ctor := findField(schemaType(merged, "Query"), "greeter")
	require.NotNil(t, ctor)
	require.Len(t, ctor.Args, 1)
	require.Equal(t, "prefix", ctor.Args[0].Name)
	require.NotNil(t, ctor.Directives.Directive(sourceModuleDirectiveName))

	// The reused constructor field also carries @sourceMap so the codegen
	// file-splitter can route it into internal/dagger/greeter.gen.go.
	ctorSM := ctor.Directives.SourceMap()
	require.NotNil(t, ctorSM, "@sourceMap directive must be present on reused constructor field")
	require.Equal(t, "greeter", ctorSM.Module)
}

func TestSchemaMergeIdempotent(t *testing.T) {
	once, err := mustSchema(t, baseSchemaJSON).Merge(JSON(echoModuleJSON), "echo")
	require.NoError(t, err)
	// Re-merging the same module must be a no-op, as the multi-pass
	// codegen loop reuses the schema across passes.
	twice, err := once.Merge(JSON(echoModuleJSON), "echo")
	require.NoError(t, err)

	require.Equal(t, schemaTypeNames(once, ""), schemaTypeNames(twice, ""))
	var echoFields int
	for _, f := range schemaType(twice, "Query").Fields {
		if f.Name == "echo" {
			echoFields++
		}
	}
	require.Equal(t, 1, echoFields)
}

func TestSchemaMergeConflict(t *testing.T) {
	_, err := mustSchema(t, baseSchemaJSON).Merge(JSON(conflictModuleJSON), "conflicting")
	require.ErrorContains(t, err, "already exists")
}

func TestSchemaMergeRequiresModuleName(t *testing.T) {
	_, err := mustSchema(t, baseSchemaJSON).Merge(JSON(echoModuleJSON), "")
	require.ErrorContains(t, err, "module name is required")
}

func TestSchemaMergeInterfaceAndEnum(t *testing.T) {
	t.Run("interface", func(t *testing.T) {
		merged, err := mustSchema(t, baseSchemaJSON).Merge(JSON(zooModuleJSON), "zoo")
		require.NoError(t, err)
		animal := schemaType(merged, "Animal")
		require.NotNil(t, animal)
		require.Equal(t, codegenintrospection.TypeKindInterface, animal.Kind)
		require.NotNil(t, animal.Directives.Directive(sourceModuleDirectiveName))
		sm := animal.Directives.SourceMap()
		require.NotNil(t, sm, "@sourceMap directive must be present on merged interface type")
		require.Equal(t, "zoo", sm.Module)
	})
	t.Run("enum", func(t *testing.T) {
		merged, err := mustSchema(t, baseSchemaJSON).Merge(JSON(workflowModuleJSON), "workflow")
		require.NoError(t, err)
		status := schemaType(merged, "Status")
		require.NotNil(t, status)
		require.Equal(t, codegenintrospection.TypeKindEnum, status.Kind)
		require.Len(t, status.EnumValues, 2)
		sm := status.Directives.SourceMap()
		require.NotNil(t, sm, "@sourceMap directive must be present on merged enum type")
		require.Equal(t, "workflow", sm.Module)
	})
}

func TestSchemaContentsRoundTrip(t *testing.T) {
	s := mustSchema(t, baseSchemaJSON)
	data, err := s.Contents()
	require.NoError(t, err)

	reparsed, err := NewSchema(data)
	require.NoError(t, err)
	require.ElementsMatch(t, schemaTypeNames(s, ""), schemaTypeNames(reparsed, ""))
}
