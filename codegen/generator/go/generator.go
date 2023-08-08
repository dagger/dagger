package gogenerator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"strings"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/go/templates"
	"github.com/dagger/dagger/codegen/introspection"
)

type GoGenerator struct {
	Config generator.Config
}

func (g *GoGenerator) Generate(ctx context.Context, schema *introspection.Schema) ([]byte, error) {
	generator.SetSchema(schema)

	headerData := struct {
		Package string
		Schema  *introspection.Schema
	}{
		Package: g.Config.Package,
		Schema:  schema,
	}

	envNames := map[string]struct{}{}
	if len(g.Config.Environments) > 0 {
		// TODO:
		generator.QueryStructClientName = "daggerClient"

		for _, env := range g.Config.Environments {
			name, err := env.Name(ctx)
			if err != nil {
				return nil, err
			}
			envNames[name] = struct{}{}
		}
	}

	headerTmpl := templates.Header
	if len(g.Config.Environments) > 0 {
		headerTmpl = templates.EnvironmentHeader
	}
	var header bytes.Buffer
	if err := headerTmpl.Execute(&header, headerData); err != nil {
		return nil, err
	}

	render := []string{
		header.String(),
	}

	err := schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			// TODO:
			if len(g.Config.Environments) > 0 {
				return nil
			}

			var out bytes.Buffer
			if err := templates.Scalar.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			objectTmpl := templates.Object
			if len(g.Config.Environments) > 0 {
				objectTmpl = templates.EnvironmentObject

				// TODO: hacks on hacks
				if t.Name == "Query" {
					// only include fields for the environments being codegen'd
					newFields := make([]*introspection.Field, 0, 1)
					for _, f := range t.Fields {
						if _, ok := envNames[f.Name]; ok {
							newFields = append(newFields, f)
						}
					}
					t.Fields = newFields
				} else {
					// otherwise only include object for the environment
					_, ok := envNames[strings.ToLower(t.Name[:1])+t.Name[1:]]
					if !ok {
						return nil
					}
				}
			}

			var out bytes.Buffer
			if err := objectTmpl.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Enum: func(t *introspection.Type) error {
			// TODO:
			if len(g.Config.Environments) > 0 {
				return nil
			}

			var out bytes.Buffer
			if err := templates.Enum.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Input: func(t *introspection.Type) error {
			// TODO:
			if len(g.Config.Environments) > 0 {
				return nil
			}

			var out bytes.Buffer
			if err := templates.Input.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	formatted, err := format.Source(
		[]byte(strings.Join(render, "\n")),
	)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	return formatted, nil
}
