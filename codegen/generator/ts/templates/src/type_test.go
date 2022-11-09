package test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestType(t *testing.T) {
	tmpl := templateHelper(t, "object", "object_comment", "field", "input_args", "arg", "return", "types", "type")

	object := objectInit(t, fieldArgsTypeJSON)

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "type", object)

	want := expectedFieldArgsType

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

var expectedFieldArgsType = `export type ContainerBuildArgs = {
  context: DirectoryID;
  dockerfile?: string;
};`

var fieldArgsTypeJSON = `
      {
        "kind": "OBJECT",
        "name": "Container",
        "description": "",
        "fields": [
          {
            "name": "build",
            "description": "",
            "args": [
              {
                "name": "context",
                "description": "",
                "type": {
                  "kind": "NON_NULL",
                  "name": null,
                  "ofType": {
                    "kind": "SCALAR",
                    "name": "DirectoryID",
                    "ofType": null
                  }
                },
                "defaultValue": null
              },
              {
                "name": "dockerfile",
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
        ]
    }
`
