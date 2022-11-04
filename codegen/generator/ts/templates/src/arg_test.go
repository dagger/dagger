package test

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/dagger/dagger/codegen/generator/ts/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestArg(t *testing.T) {
	tmpl := template.Must(template.New("arg").Funcs(templates.FuncMap).ParseFiles("arg.ts.tmpl"))

	arg := introspection.Field{
		Name: "ref",
		TypeRef: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "string",
		},
	}
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "arg", arg)

	want := "string ref"

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}
