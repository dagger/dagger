package templates

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestObjectOptionalArgsDeprecatedNoDescription(t *testing.T) {
	schemaJSON := `
    {
      "description": "Container with deprecated args",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "",
              "isDeprecated": true,
              "deprecationReason": "Templates are expanded automatically.",
              "name": "expand",
              "type": {
                "kind": "SCALAR",
                "name": "Boolean"
              }
            }
          ],
          "deprecationReason": null,
          "description": "Apply configuration to the container",
          "isDeprecated": false,
          "name": "apply",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "OBJECT",
              "name": "Container"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Container"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	want := updateAndGetFixture(t, "testdata/object_optional_args_deprecated_no_description.golden", got)

	require.Equal(t, want, got)
}

func TestObjectFieldsTemplateOptionalIDArgUsesExpectedType(t *testing.T) {
	schemaJSON := `
    {
      "description": "Root query",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "Project source directory",
              "isDeprecated": false,
              "deprecationReason": null,
              "name": "source",
              "type": {
                "kind": "SCALAR",
                "name": "ID"
              },
              "directives": [
                {
                  "name": "expectedType",
                  "args": [
                    {
                      "name": "name",
                      "value": "\"Directory\""
                    }
                  ]
                }
              ]
            }
          ],
          "deprecationReason": null,
          "description": "A Go project",
          "isDeprecated": false,
          "name": "go",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "OBJECT",
              "name": "Go"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Query"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object_fields.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	require.Contains(t, got, "Source *Directory")
	require.NotContains(t, got, "Source ID")
}

func TestObjectLegacyIDTypeUsesFormattedGoName(t *testing.T) {
	schemaJSON := `
    {
      "description": "LLM test module",
      "fields": [
        {
          "args": [],
          "deprecationReason": null,
          "description": "A unique identifier for this LlmTestModule.",
          "isDeprecated": false,
          "name": "id",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "ID"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "LlmTestModule"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	require.Contains(t, got, "id *LLMTestModuleID")
	require.Contains(t, got, "func (r *LLMTestModule) ID(ctx context.Context) (LLMTestModuleID, error)")
	require.NotContains(t, got, "LlmTestModuleID")
}

func TestObjectMethodDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Container with deprecated method",
      "fields": [
        {
          "args": [],
          "deprecationReason": "Use ApplyV2 instead.",
          "description": "Apply configuration to the container",
          "isDeprecated": true,
          "name": "apply",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "OBJECT",
              "name": "Container"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Container"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	want := updateAndGetFixture(t, "testdata/object_method_deprecated.golden", got)

	require.Equal(t, want, got)
}

func TestObjectFieldDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Test object with deprecated field",
      "fields": [
        {
          "args": [],
          "deprecationReason": "This field is deprecated and will be removed in future versions.",
          "description": "",
          "isDeprecated": true,
          "name": "legacyField",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Test"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	want := updateAndGetFixture(t, "testdata/object_field_deprecated.golden", got)

	require.Equal(t, want, got)
}

func TestInterfaceMethodOptionalArgDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Test interface with deprecated method",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "deprecationReason": "Not needed anymore.",
              "description": "",
              "isDeprecated": true,
              "name": "bar",
              "type": {
                "kind": "SCALAR",
                "name": "Int"
              }
            }
          ],
          "deprecationReason": "Use Bar instead.",
          "description": "",
          "isDeprecated": true,
          "name": "foo",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        }
      ],
      "kind": "INTERFACE",
      "name": "TestFooer"
    }
`

	schema, iface := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, iface)

	want := updateAndGetFixture(t, "testdata/interface_method_optional_arg_deprecated.golden", got)

	require.Equal(t, want, got)
}

// idHandleSchemaJSON has an object whose ID-returning fields name its own
// type (sync), another object (spawn), and an interface (syncer).
const idHandleSchemaJSON = `
[
  {"kind": "SCALAR", "name": "ID"},
  {"kind": "INTERFACE", "name": "Syncer", "description": "", "fields": []},
  {"kind": "OBJECT", "name": "Agent", "description": "", "fields": []},
  {
    "kind": "OBJECT",
    "name": "LLM",
    "description": "",
    "fields": [
      {
        "args": [],
        "description": "",
        "name": "sync",
        "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}},
        "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"LLM\""}]}]
      },
      {
        "args": [],
        "description": "",
        "name": "spawn",
        "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}},
        "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Agent\""}]}]
      },
      {
        "args": [],
        "description": "",
        "name": "syncer",
        "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}},
        "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Syncer\""}]}]
      }
    ]
  }
]
`

func renderIDHandleObject(t *testing.T, schemaVersion string) string {
	t.Helper()
	var types introspection.Types
	require.NoError(t, json.Unmarshal([]byte(idHandleSchemaJSON), &types))
	schema := &introspection.Schema{Types: types}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })

	funcs := GoTemplateFuncs(t.Context(), schema, nil, schemaVersion, generator.Config{}, nil, nil, 0)
	path := filepath.Join("src", "_types", "object.go.tmpl")
	tmpl, err := template.New("object.go.tmpl").Funcs(funcs).ParseFiles(path)
	require.NoError(t, err)
	return renderTemplate(t, tmpl.Lookup("object.go.tmpl"), schema.Types.Get("LLM"))
}

func TestObjectIDHandlesLoadTheirExpectedType(t *testing.T) {
	got := renderIDHandleObject(t, "v1.0.0")

	// the parent's own ID keeps loading the parent
	require.Contains(t, got, "func (r *LLM) Sync(ctx context.Context) (*LLM, error) {")
	require.Contains(t, got, `return &LLM{
		query: selectNode(q.Root(), id, "LLM"),
	}, nil`)
	// another object's ID loads that object
	require.Contains(t, got, "func (r *LLM) Spawn(ctx context.Context) (*Agent, error) {")
	require.Contains(t, got, `return &Agent{
		query: selectNode(q.Root(), id, "Agent"),
	}, nil`)
	// an interface's ID loads through the interface's client struct
	require.Contains(t, got, "func (r *LLM) Syncer(ctx context.Context) (Syncer, error) {")
	require.Contains(t, got, `return &SyncerClient{
		query: selectNode(q.Root(), id, "Syncer"),
	}, nil`)
}
