package templates

import (
	"context"
	"testing"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/stretchr/testify/require"
)

var currentSchema *introspection.Schema

func init() {
	ctx := context.Background()

	engineConf := &engine.Config{
		RunnerHost:   internalengine.RunnerHost(),
		NoExtensions: true,
	}
	err := engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		var err error
		currentSchema, err = generator.Introspect(ctx, r)
		if err != nil {
			panic(err)
		}
		generator.SetSchemaParents(currentSchema)
		return nil
	})
	if err != nil {
		panic(err)
	}
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

func TestSortInputFields(t *testing.T) {
	genInput := func(names []string) []introspection.InputValue {
		var iv []introspection.InputValue
		for _, i := range names {
			iv = append(iv, introspection.InputValue{
				Name: i,
			})
		}
		return iv
	}

	t.Run("name, value", func(t *testing.T) {
		names := []string{"name", "value"}

		iv := genInput(names)

		want := make([]introspection.InputValue, len(iv))
		copy(want, iv)

		got := sortInputFields(want)
		require.Equal(t, want, got)
	})
	t.Run("value, name", func(t *testing.T) {
		names := []string{"value", "name"}
		iv := genInput(names)

		want := genInput([]string{"name", "value"})

		got := sortInputFields(iv)
		require.Equal(t, want, got)
	})

	t.Run("a, z, b, t, l", func(t *testing.T) {
		names := []string{"a", "z", "b", "t", "l"}
		iv := genInput(names)

		want := genInput([]string{"a", "b", "l", "t", "z"})

		got := sortInputFields(iv)
		require.Equal(t, want, got)
	})
}
