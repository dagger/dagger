package test

import (
	"bytes"
	"testing"

	generator "github.com/dagger/dagger/codegen/generator/go"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestField(t *testing.T) {
	const templateType = "field"
	t.Run("myField()", func(t *testing.T) {
		tmpl := templateHelper(t, templateType, "input_args", "arg", "return")
		want := `myField() {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'myField',
      args
      }
    ]
  }`

		field := introspection.Field{
			Name: "myField",
		}
		fieldInit(t, &field)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, field)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})

	t.Run("exec(args: ContainerExecArgs) : Container", func(t *testing.T) {
		tmpl := templateHelper(t, templateType, "input_args", "arg", "return")
		want := `exec(args: ContainerExecArgs) : Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ]

    return new Container(this._queryTree)
  }`
		object := objectInit(t, containerExecArgsJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, object.Fields[0])
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})

	t.Run("async id() : Promise<Record<string, DirectoryID>>", func(t *testing.T) {
		tmpl := templateHelper(t, templateType, "input_args", "arg", "return", "return_solve")
		want := `async id() : Promise<Record<string, DirectoryID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: new Promise<Record<string, Scalars('DirectoryID')>> = await this._compute()

    return response
  }`
		object := objectInit(t, directoryTypeJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, object.Fields[0])
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
}

const directoryTypeJSON = `{
        "kind": "OBJECT",
        "name": "Directory",
        "description": "",
        "fields": [
          {
            "name": "id",
            "description": "",
            "args": [],
            "type": {
              "kind": "NON_NULL",
              "name": null,
              "ofType": {
                "kind": "SCALAR",
                "name": "DirectoryID",
                "ofType": null
              }
            },
            "isDeprecated": false,
            "deprecationReason": null
          }
	]
	}`

func fieldInit(t *testing.T, field *introspection.Field) {
	t.Helper()

	schema := introspection.Schema{
		Types: []*introspection.Type{
			{
				Kind: introspection.TypeKindObject,
				Name: "Whatever",
				Fields: []*introspection.Field{
					field,
				},
			},
		},
	}
	generator.SetSchemaParents(&schema)
}
