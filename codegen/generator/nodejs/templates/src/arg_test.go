package test

import (
	"bytes"
	"testing"

	"github.com/dagger/dagger/codegen/generator/nodejs/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestArg(t *testing.T) {
	tmpl := templates.New()

	arg := introspection.Field{
		Name: "ref",
		TypeRef: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "string",
		},
	}
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "arg", arg)

	want := "ref?: string"

	require.NoError(t, err)
	require.Equal(t, want, b.String())
}
