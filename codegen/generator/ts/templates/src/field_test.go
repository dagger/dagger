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
		want := `myField()`
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
		want := `exec(args: ContainerExecArgs) : Container`
		object := objectInit(t, containerExecArgsJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, object.Fields[0])
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})

	t.Run("exec(args: ContainerExecArgs) : Promise<Return<string, Container>>", func(t *testing.T) {
		tmpl := templateHelper(t, templateType, "input_args", "arg", "return")
		want := `exec(args: ContainerExecArgs) : Container`
		object := objectInit(t, containerExecArgsJSON)

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, object.Fields[0])
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
}

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
