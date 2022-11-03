package test

import (
	"bytes"
	"testing"

	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestField(t *testing.T) {
	t.Run("myField()", func(t *testing.T) {
		templateType := "field"
		tmpl := templateHelper(t, templateType, "args", "arg", "return")
		want := `myField()`
		field := introspection.Field{
			Name: "myField",
			TypeRef: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: "",
			},
		}

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, field)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})

	t.Run("myField(string ref) : Container", func(t *testing.T) {
		templateType := "field"
		tmpl := templateHelper(t, templateType, "args", "arg", "return")
		want := `myField(string ref) : Container`
		field := introspection.Field{
			Name: "myField",
			TypeRef: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: "Container",
			},
			Args: introspection.InputValues{
				introspection.InputValue{
					Name: "ref",
					TypeRef: &introspection.TypeRef{
						Kind: introspection.TypeKindScalar,
						Name: "string",
					},
				},
			},
		}

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, field)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
}
