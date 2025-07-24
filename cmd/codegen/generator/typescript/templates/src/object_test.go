package test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
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
