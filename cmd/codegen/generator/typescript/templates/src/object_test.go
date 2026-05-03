package test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestObject(t *testing.T) {
	tmpl := templateHelper(t)

	object := objectInit(t, containerExecArgsJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)

	want := updateAndGetFixtures(t, "testdata/object_test_want.ts", b.String())
	require.NoError(t, err)

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

func TestObjectCasingConsistency(t *testing.T) {
	tmpl := templateHelper(t)

	object := objectInit(t, JSONValueJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)
	require.NoError(t, err)

	want := updateAndGetFixtures(t, "testdata/object_json_test_want.ts", b.String())

	require.Equal(t, want, b.String())
}

func TestObjectFieldDeprecated(t *testing.T) {
	tmpl := templateHelper(t)

	var objectFieldDeprecatedJSON = `
    {
      "kind": "OBJECT",
      "name": "Test",
      "description": "",
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
      "inputFields": null,
      "interfaces": [],
      "enumValues": null,
      "possibleTypes": null
    }
`

	object := objectInit(t, objectFieldDeprecatedJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)
	require.NoError(t, err)

	want := updateAndGetFixtures(t, "testdata/object_field_deprecated_want.ts", b.String())

	require.Equal(t, want, b.String())
}

func TestInterfaceMethodOptionalArgDeprecated(t *testing.T) {
	tmpl := templateHelper(t)

	var interfaceDeprecatedJSON = `
    {
      "kind": "INTERFACE",
      "name": "TestFooer",
      "description": "",
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
      "inputFields": null,
      "interfaces": [],
      "enumValues": null,
      "possibleTypes": null
    }
`

	object := objectInit(t, interfaceDeprecatedJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", object)
	require.NoError(t, err)

	want := updateAndGetFixtures(t, "testdata/interface_method_optional_arg_deprecated_want.ts", b.String())

	require.Equal(t, want, b.String())
}

var legacyUnifiedIDSchemaJSON = `
[
  {
    "kind": "SCALAR",
    "name": "ID",
    "description": "",
    "fields": null,
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "SCALAR",
    "name": "String",
    "description": "",
    "fields": null,
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "INTERFACE",
    "name": "Node",
    "description": "",
    "fields": [
      {
        "name": "id",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": [
          { "name": "expectedType", "args": [{ "name": "name", "value": "\"Node\"" }] }
        ]
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "INTERFACE",
    "name": "DepCustomIface",
    "description": "",
    "fields": [
      {
        "name": "id",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": [
          { "name": "expectedType", "args": [{ "name": "name", "value": "\"DepCustomIface\"" }] }
        ]
      },
      {
        "name": "str",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } },
        "isDeprecated": false,
        "deprecationReason": null
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "OBJECT",
    "name": "Container",
    "description": "",
    "fields": [
      {
        "name": "id",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": [
          { "name": "expectedType", "args": [{ "name": "name", "value": "\"Container\"" }] }
        ]
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "OBJECT",
    "name": "File",
    "description": "",
    "fields": [
      {
        "name": "id",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": [
          { "name": "expectedType", "args": [{ "name": "name", "value": "\"File\"" }] }
        ]
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  },
  {
    "kind": "OBJECT",
    "name": "Query",
    "description": "",
    "fields": [
      {
        "name": "container",
        "description": "",
        "args": [],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "OBJECT", "name": "Container" } },
        "isDeprecated": false,
        "deprecationReason": null
      },
      {
        "name": "file",
        "description": "",
        "args": [
          {
            "name": "id",
            "description": "",
            "defaultValue": null,
            "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
            "directives": [
              { "name": "expectedType", "args": [{ "name": "name", "value": "\"File\"" }] }
            ],
            "isDeprecated": false,
            "deprecationReason": null
          }
        ],
        "type": { "kind": "NON_NULL", "ofType": { "kind": "OBJECT", "name": "File" } },
        "isDeprecated": false,
        "deprecationReason": null
      }
    ],
    "inputFields": null,
    "interfaces": [],
    "enumValues": null,
    "possibleTypes": null
  }
]
`

func TestLegacyTypeScriptIDFacade(t *testing.T) {
	schema := objectsInit(t, legacyUnifiedIDSchemaJSON)
	generator.SetSchema(&schema)
	t.Cleanup(func() { generator.SetSchema(nil) })

	got := renderAPI(t, &schema, "v0.20.6")

	require.Contains(t, got, "export type ContainerID = string & { __ContainerID: never }")
	require.Contains(t, got, "export type DepCustomIfaceID = string & { __DepCustomIfaceID: never }")
	require.NotContains(t, got, "export type NodeID")
	require.Contains(t, got, "id(): Promise<ID>")
	require.Contains(t, got, "private readonly _id?: ContainerID = undefined")
	require.Contains(t, got, "id = async (): Promise<ContainerID> => {")
	require.Contains(t, got, "const response: Awaited<ContainerID> = await ctx.execute()")
	require.Contains(t, got, "file = (id: FileID): File => {")
	require.Contains(t, got, "loadContainerFromID = (id: ContainerID): Container => {")
	require.Contains(t, got, `const ctx = this._ctx.selectNode(id, "Container")`)
	require.Contains(t, got, "return new Container(ctx)")
	require.Contains(t, got, "loadDepCustomIfaceFromID = (id: DepCustomIfaceID): DepCustomIface => {")
	require.Contains(t, got, `const ctx = this._ctx.selectNode(id, "DepCustomIface")`)
	require.Contains(t, got, "return new _DepCustomIfaceClient(ctx)")
	require.NotContains(t, got, `this._ctx.select("loadContainerFromID"`)
}

func TestModernTypeScriptIDSurface(t *testing.T) {
	schema := objectsInit(t, legacyUnifiedIDSchemaJSON)
	generator.SetSchema(&schema)
	t.Cleanup(func() { generator.SetSchema(nil) })

	got := renderAPI(t, &schema, "v0.21.0-dev")

	require.NotContains(t, got, "export type ContainerID")
	require.NotContains(t, got, "loadContainerFromID")
	require.Contains(t, got, "private readonly _id?: ID = undefined")
	require.Contains(t, got, "id = async (): Promise<ID> => {")
	require.Contains(t, got, "const response: Awaited<ID> = await ctx.execute()")
}

func renderAPI(t *testing.T, schema *introspection.Schema, schemaVersion string) string {
	t.Helper()
	tmpl := templates.New(schemaVersion, generator.Config{})
	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
	}{
		Schema:        schema,
		SchemaVersion: schemaVersion,
		Types:         schema.Types,
	}
	var b bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&b, "api", data))
	return b.String()
}

func TestInterfaceConvertIDMethodConstructsClient(t *testing.T) {
	tmpl := templateHelper(t)

	var syncerJSON = `
    {
      "kind": "INTERFACE",
      "name": "Syncer",
      "description": "",
      "fields": [
        {
          "args": [],
          "deprecationReason": null,
          "description": "",
          "isDeprecated": false,
          "name": "sync",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "ID"
            }
          },
          "directives": [
            {
              "name": "expectedType",
              "args": [
                {
                  "name": "name",
                  "value": "\"Syncer\""
                }
              ]
            }
          ]
        }
      ],
      "inputFields": null,
      "interfaces": [],
      "enumValues": null,
      "possibleTypes": null
    }
`

	object := objectInit(t, syncerJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "interface", object)
	require.NoError(t, err)

	got := b.String()
	require.Contains(t, got, "sync = async (): Promise<Syncer> => {")
	require.Contains(t, got, `return new _SyncerClient(ctx.copy().selectNode(response, "Syncer"))`)
	require.NotContains(t, got, `return new Syncer(`)
}

func objectInit(t *testing.T, jsonString string) *introspection.Type {
	t.Helper()
	var object introspection.Type
	err := json.Unmarshal([]byte(jsonString), &object)
	require.NoError(t, err)

	schema := introspection.Schema{
		Types: []*introspection.Type{
			&object,
		},
	}

	generator.SetSchemaParents(&schema)
	return &object
}

func objectsInit(t *testing.T, jsonString string) introspection.Schema {
	t.Helper()
	var objects introspection.Types
	err := json.Unmarshal([]byte(jsonString), &objects)
	require.NoError(t, err)

	schema := introspection.Schema{
		Types: objects,
	}

	generator.SetSchemaParents(&schema)
	return schema
}

var JSONValueJSON = `
{
  "kind": "OBJECT",
  "name": "JSONValue",
  "description": "",
  "fields": [
    {
      "name": "bytes",
      "description": "",
      "args": [
        {
           "name": "pretty",
           "description": "",
           "type": {
             "kind": "SCALAR",
             "name": "Boolean",
             "ofType": null
           },
           "defaultValue": null
        },
        {
          "name": "indent",
          "description": "",
          "type": {
            "kind": "SCALAR",
            "name": "Int",
            "ofType": null
          },
          "defaultValue": null
        }
      ],
      "type": {
        "kind": "NON_NULL",
        "name": null,
        "ofType": {
          "kind": "SCALAR",
          "name": "JSON",
          "ofType": null
        }
      },
      "isDeprecated": false,
      "deprecationReason": null
    }
  ],
  "inputFields": null,
  "interfaces": [],
  "enumValues": null,
  "possibleTypes": null
}
`

var containerExecArgsJSON = `
      {
        "kind": "OBJECT",
        "name": "Container",
        "description": "",
        "fields": [
          {
            "name": "exec",
            "description": "",
            "args": [
              {
                "name": "args",
                "description": "",
                "type": {
                  "kind": "LIST",
                  "name": null,
                  "ofType": {
                    "kind": "NON_NULL",
                    "name": null,
                    "ofType": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  }
                },
                "defaultValue": null
              },
              {
                "name": "stdin",
                "description": "",
                "type": {
                  "kind": "SCALAR",
                  "name": "String",
                  "ofType": null
                },
                "defaultValue": null
              },
              {
                "name": "redirectStdout",
                "description": "",
                "type": {
                  "kind": "SCALAR",
                  "name": "String",
                  "ofType": null
                },
                "defaultValue": null
              },
              {
                "name": "redirectStderr",
                "description": "",
                "type": {
                  "kind": "SCALAR",
                  "name": "String",
                  "ofType": null
                },
                "defaultValue": null
              }
            ],
            "type": {
              "kind": "NON_NULL",
              "name": null,
              "ofType": {
                "kind": "OBJECT",
                "name": "Container",
                "ofType": null
              }
            },
            "isDeprecated": false,
            "deprecationReason": null
          }
	],
        "inputFields": null,
        "interfaces": [],
        "enumValues": null,
        "possibleTypes": null
      }
`
