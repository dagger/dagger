package test

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/ts/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestObject(t *testing.T) {
	tmpl := template.Must(template.New("object").Funcs(templates.FuncMap).ParseFiles("object.ts.tmpl"))

	var b bytes.Buffer
	type Arg struct {
		Name string
		Type string
	}
	type Args struct {
		Args         []Arg
		HasOptionals bool
	}
	type Field struct {
		Name        string
		Type        string
		Description string
		Args        Args
	}
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
				Name: "Field1", TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "string"}, Args: introspection.InputValues{
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
class ref extends BaseApi {

  get getQueryTree() {
    return this._queryTree;
  }


}
`
