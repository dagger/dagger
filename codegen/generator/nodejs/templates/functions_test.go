package templates

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/stretchr/testify/require"
)

var currentSchema *introspection.Schema

func init() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	currentSchema, err = generator.Introspect(ctx, client)
	if err != nil {
		panic(err)
	}
	generator.SetSchemaParents(currentSchema)
}

func getField(t *introspection.Type, name string) *introspection.Field {
	for _, v := range t.Fields {
		if v.Name == name {
			return v
		}
	}
	return nil
}

func TestSplitRequiredOptionalArgs(t *testing.T) {
	t.Run("container exec", func(t *testing.T) {
		container := currentSchema.Types.Get("Container")
		require.NotNil(t, container)
		execField := getField(container, "exec")

		t.Log(container)
		required, optional := splitRequiredOptionalArgs(execField.Args)
		require.Equal(t, execField.Args[:0], required)
		require.Equal(t, execField.Args, optional)
	})
	t.Run("container export", func(t *testing.T) {
		container := currentSchema.Types.Get("Container")
		require.NotNil(t, container)
		execField := getField(container, "export")

		t.Log(container)
		required, optional := splitRequiredOptionalArgs(execField.Args)
		require.Equal(t, execField.Args[:1], required)
		require.Equal(t, execField.Args[1:], optional)
	})
}
