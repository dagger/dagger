package elixirgenerator

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/elixir/templates"
	"github.com/dagger/dagger/codegen/introspection"
)

type ElixirGenerator struct{}

func (g *ElixirGenerator) Generate(ctx context.Context, schema *introspection.Schema) ([]byte, error) {
	generator.SetSchema(schema)

	modules := []string{}

	schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Scalar.Execute(&out, t); err != nil {
				return err
			}
			modules = append(modules, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Object.Execute(&out, t); err != nil {
				return err
			}
			modules = append(modules, out.String())
			return nil
		},
		Enum: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Enum.Execute(&out, t); err != nil {
				return err
			}
			modules = append(modules, out.String())
			return nil
		},
		Input: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Input.Execute(&out, t); err != nil {
				fmt.Println(err)
				return err
			}
			modules = append(modules, out.String())
			return nil
		},
	})

	return []byte(strings.Join(modules, "\n\n")), nil
}
