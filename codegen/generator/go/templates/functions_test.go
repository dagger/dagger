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

func TestSortEnumFields(t *testing.T) {
	genInput := func(names []string) []introspection.EnumValue {
		var iv []introspection.EnumValue
		for _, i := range names {
			iv = append(iv, introspection.EnumValue{
				Name: i,
			})
		}
		return iv
	}

	t.Run("name, value", func(t *testing.T) {
		names := []string{"name", "value"}

		iv := genInput(names)

		want := make([]introspection.EnumValue, len(iv))
		copy(want, iv)

		got := sortEnumFields(want)
		require.Equal(t, want, got)
	})
	t.Run("value, name", func(t *testing.T) {
		names := []string{"value", "name"}
		iv := genInput(names)

		want := genInput([]string{"name", "value"})

		got := sortEnumFields(iv)
		require.Equal(t, want, got)
	})

	t.Run("a, z, b, t, l", func(t *testing.T) {
		names := []string{"a", "z", "b", "t", "l"}
		iv := genInput(names)

		want := genInput([]string{"a", "b", "l", "t", "z"})

		got := sortEnumFields(iv)
		require.Equal(t, want, got)
	})
}
