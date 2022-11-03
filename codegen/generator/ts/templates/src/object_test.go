package test

import (
	"bytes"
	"testing"

	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestObject(t *testing.T) {
	tmpl := templateHelper(t, "object", "comment", "field", "args", "arg")

	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "object", struct {
		Name        string
		Type        string
		Description string
		Fields      []introspection.Field
	}{
		Type:        "string",
		Name:        "ref",
		Description: "this is the description",
		Fields: []introspection.Field{
			{
				// TODO improve so Field1 becomes field1 : check with introspection
				Name: "Field1", TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "string"}, Args: introspection.InputValues{
					// TODO improve so Arg1 becomes field1 : check with introspection
					{Name: "Arg1", TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "string"}},
				},
			},
		},
	})

	want := expectedFunc

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}

var expectedFunc = `
/**
 * this is the description
 */
class ref extends BaseApi {

  get getQueryTree() {
    return this._queryTree;
  }

  async Field1(string Arg1) : Promise<Record<string, string>>
}
`
