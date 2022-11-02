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
		tmpl := templateHelper(t, templateType, "args", "arg")
		want := `myField()`
		field := introspection.Field{
			Name: "myField",
			TypeRef: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: "function",
			},
		}

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, templateType, field)
		require.NoError(t, err)

		require.Equal(t, want, b.String())
	})
}
