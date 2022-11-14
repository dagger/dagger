package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputArgs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"ContainerExecArgs": {containerExecArgsJSON, "args?: ContainerExecArgs"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := templateHelper(t)

			jsonData := c.in

			object := objectInit(t, jsonData)

			var b bytes.Buffer
			err := tmpl.ExecuteTemplate(&b, "input_args", object.Fields[0])

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
