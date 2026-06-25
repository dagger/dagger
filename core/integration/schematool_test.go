package core

// These tests exercise the engine's schemaTools surface: the `schema(json)`
// constructor plus the Schema object's `merge` and `contents` operations.
// They issue GraphQL directly and assert on the merged introspection JSON
// returned by `contents`.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
)

type SchemaToolsSuite struct{}

func TestSchemaTools(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SchemaToolsSuite{})
}

// baseSchemaToolsJSON is the introspection schema used as the merge target.
// It is a minimal valid response with a Query root and one pre-existing
// object.
const baseSchemaToolsJSON = `{
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

// echoSchemaToolsModuleJSON declares a single object with no Query type, so
// merge must synthesize a no-arg constructor field on Query.
const echoSchemaToolsModuleJSON = `{
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

// introspection JSON shapes used to assert on merged `contents`.
type introDirective struct {
	Name string `json:"name"`
	Args []struct {
		Name  string  `json:"name"`
		Value *string `json:"value"`
	} `json:"args"`
}

type introField struct {
	Name string `json:"name"`
	Type struct {
		Kind   string `json:"kind"`
		OfType *struct {
			Kind string  `json:"kind"`
			Name *string `json:"name"`
		} `json:"ofType"`
	} `json:"type"`
	Directives []introDirective `json:"directives"`
}

type introType struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Fields      []introField     `json:"fields"`
	Directives  []introDirective `json:"directives"`
}

type introSchema struct {
	Schema struct {
		Types []introType `json:"types"`
	} `json:"__schema"`
}

func parseSchemaContents(t *testctx.T, contents string) introSchema {
	t.Helper()
	var s introSchema
	require.NoError(t, json.Unmarshal([]byte(contents), &s))
	return s
}

func (s introSchema) typeNames() []string {
	names := make([]string, 0, len(s.Schema.Types))
	for _, t := range s.Schema.Types {
		names = append(names, t.Name)
	}
	return names
}

func (s introSchema) findType(name string) *introType {
	for i, t := range s.Schema.Types {
		if t.Name == name {
			return &s.Schema.Types[i]
		}
	}
	return nil
}

func hasSourceModuleStamp(directives []introDirective, encodedName string) bool {
	for _, d := range directives {
		if d.Name != "sourceModuleName" {
			continue
		}
		for _, a := range d.Args {
			if a.Name == "name" && a.Value != nil && *a.Value == encodedName {
				return true
			}
		}
	}
	return false
}

// mergeContents merges a module into the base schema and returns the parsed
// merged introspection JSON.
func mergeContents(t *testctx.T, base, module, moduleName string) introSchema {
	t.Helper()
	res, err := testutil.Query[struct {
		Schema struct {
			Merged struct {
				Contents string `json:"contents"`
			} `json:"merged"`
		} `json:"schema"`
	}](t, `query Merge($base: JSON!, $module: JSON!, $name: String!) {
		schema(json: $base) {
			merged: merge(moduleTypes: $module, moduleName: $name) {
				contents
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"base":   base,
		"module": module,
		"name":   moduleName,
	}})
	require.NoError(t, err)
	return parseSchemaContents(t, res.Schema.Merged.Contents)
}

func (SchemaToolsSuite) TestMerge(ctx context.Context, t *testctx.T) {
	merged := mergeContents(t, baseSchemaToolsJSON, echoSchemaToolsModuleJSON, "echo")

	require.ElementsMatch(t, []string{"Query", "Container", "Echo"}, merged.typeNames())

	echo := merged.findType("Echo")
	require.NotNil(t, echo)
	require.Equal(t, "OBJECT", echo.Kind)
	require.Equal(t, "Echo object", echo.Description)
	require.True(t, hasSourceModuleStamp(echo.Directives, `"echo"`),
		"Echo type should carry @sourceModuleName")

	query := merged.findType("Query")
	require.NotNil(t, query)
	var ctor *introField
	for i, f := range query.Fields {
		if f.Name == "echo" {
			ctor = &query.Fields[i]
			break
		}
	}
	require.NotNil(t, ctor, "Query should have an echo constructor field")
	require.Equal(t, "NON_NULL", ctor.Type.Kind)
	require.NotNil(t, ctor.Type.OfType)
	require.Equal(t, "OBJECT", ctor.Type.OfType.Kind)
	require.NotNil(t, ctor.Type.OfType.Name)
	require.Equal(t, "Echo", *ctor.Type.OfType.Name)
	require.True(t, hasSourceModuleStamp(ctor.Directives, `"echo"`),
		"echo constructor should carry @sourceModuleName")
}

func (SchemaToolsSuite) TestMergeIdempotent(ctx context.Context, t *testctx.T) {
	// Merge twice via nested calls and assert the second merge did not
	// duplicate the Echo type or the constructor field.
	res, err := testutil.Query[struct {
		Schema struct {
			Once struct {
				Again struct {
					Contents string `json:"contents"`
				} `json:"again"`
			} `json:"once"`
		} `json:"schema"`
	}](t, `query MergeTwice($base: JSON!, $module: JSON!) {
		schema(json: $base) {
			once: merge(moduleTypes: $module, moduleName: "echo") {
				again: merge(moduleTypes: $module, moduleName: "echo") {
					contents
				}
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"base":   baseSchemaToolsJSON,
		"module": echoSchemaToolsModuleJSON,
	}})
	require.NoError(t, err)

	merged := parseSchemaContents(t, res.Schema.Once.Again.Contents)

	var echoTypeCount int
	for _, name := range merged.typeNames() {
		if name == "Echo" {
			echoTypeCount++
		}
	}
	require.Equal(t, 1, echoTypeCount, "re-merging must not duplicate the type")

	query := merged.findType("Query")
	require.NotNil(t, query)
	var echoFieldCount int
	for _, f := range query.Fields {
		if f.Name == "echo" {
			echoFieldCount++
		}
	}
	require.Equal(t, 1, echoFieldCount, "re-merging the same module must not duplicate the constructor")
}

func (SchemaToolsSuite) TestMergeConflict(ctx context.Context, t *testctx.T) {
	const conflict = `{"__schema":{"types":[{"kind":"OBJECT","name":"Container","fields":[],"interfaces":[],"directives":[]}],"directives":[]}}`
	_, err := testutil.Query[struct{}](t, `query Conflict($base: JSON!, $module: JSON!) {
		schema(json: $base) {
			merge(moduleTypes: $module, moduleName: "conflicting") {
				contents
			}
		}
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"base":   baseSchemaToolsJSON,
		"module": conflict,
	}})
	require.ErrorContains(t, err, "already exists")
}

func (SchemaToolsSuite) TestContentsRoundTrip(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Schema struct {
			Contents string `json:"contents"`
		} `json:"schema"`
	}](t, `query Contents($json: JSON!) { schema(json: $json) { contents } }`,
		&testutil.QueryOptions{Variables: map[string]any{"json": baseSchemaToolsJSON}})
	require.NoError(t, err)

	parsed := parseSchemaContents(t, res.Schema.Contents)
	require.ElementsMatch(t, []string{"Query", "Container"}, parsed.typeNames())

	// Round-trip the serialized JSON back into the engine and verify the
	// types are preserved.
	back, err := testutil.Query[struct {
		Schema struct {
			Contents string `json:"contents"`
		} `json:"schema"`
	}](t, `query RoundTrip($json: JSON!) { schema(json: $json) { contents } }`,
		&testutil.QueryOptions{Variables: map[string]any{"json": res.Schema.Contents}})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Query", "Container"}, parseSchemaContents(t, back.Schema.Contents).typeNames())
}

func (SchemaToolsSuite) TestLiveEngineSchema(ctx context.Context, t *testctx.T) {
	// Fetch the engine's own introspection JSON and round-trip it through
	// schemaTools. The Schema type itself must be present in the result, a
	// self-referential proof that this feature is installed correctly.
	live, err := testutil.Query[struct {
		File struct {
			Contents string `json:"contents"`
		} `json:"__schemaJSONFile"`
	}](t, `query LiveSchema { __schemaJSONFile { contents } }`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, live.File.Contents)

	res, err := testutil.Query[struct {
		Schema struct {
			Contents string `json:"contents"`
		} `json:"schema"`
	}](t, `query Live($json: JSON!) { schema(json: $json) { contents } }`,
		&testutil.QueryOptions{Variables: map[string]any{"json": live.File.Contents}})
	require.NoError(t, err)

	names := parseSchemaContents(t, res.Schema.Contents).typeNames()
	require.Contains(t, names, "Container")
	require.Contains(t, names, "File")
	require.Contains(t, names, "Schema", "the Schema type should be present in the live engine schema")
}
