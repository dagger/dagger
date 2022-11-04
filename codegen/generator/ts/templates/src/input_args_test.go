package test

import (
	"bytes"
	"encoding/json"
	"testing"

	generator "github.com/dagger/dagger/codegen/generator/go"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestInputArgs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"ContainerExecArgs": {containerExecArgsJSON, "args: ContainerExecArgs"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templateHelper(t, "input_args")

			jsonData := c.in

			var object introspection.Type
			err := json.Unmarshal([]byte(jsonData), &object)
			require.NoError(t, err)
			schema := introspection.Schema{
				Types: []*introspection.Type{
					&object,
				},
			}

			generator.SetSchemaParents(&schema)

			var b bytes.Buffer
			err = tmpl.ExecuteTemplate(&b, "input_args", object.Fields[0])

			require.NoError(t, err)
			require.Equal(t, c.want, b.String())
		})
	}
}

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
