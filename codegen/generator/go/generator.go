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
	isForEnv := g.Config.EnvironmentName != ""

	headerData := struct {
		Package string
		Schema  *introspection.Schema
	}{
		Package: g.Config.Package,
		Schema:  schema,
	}

	var render []string

	var header bytes.Buffer
	if isForEnv {
		// TODO: ...
		templates.EvilGlobalVarToTriggerEnvSpecificCodegen = true
		templates.EvilGlobalVarWithEnvironmentName = g.Config.EnvironmentName
		if err := templates.Environment.Execute(&header, headerData); err != nil {
			return nil, err
		}
	} else {
		if err := templates.Header.Execute(&header, headerData); err != nil {
			return nil, err
		}
	}
	render = append(render, header.String())

	err := schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Scalar.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			objectTmpl := templates.Object

			// don't create methods on query for the env itself,
			// e.g. don't create `func (r *DAG) Go() *Go` in the Go env's codegen
			if g.Config.EnvironmentName != "" && t.Name == generator.QueryStructName {
				var newFields []*introspection.Field
				for _, f := range t.Fields {
					if f.Name != g.Config.EnvironmentName {
						newFields = append(newFields, f)
					}
				}
				t.Fields = newFields
			}

			objectName := strings.ToLower(t.Name[:1]) + t.Name[1:]
			if g.Config.EnvironmentName == objectName {
				// don't generate self bindings, it's too confusing for now
				return nil
			}

			var out bytes.Buffer
			if err := objectTmpl.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Enum: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Enum.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Input: func(t *introspection.Type) error {
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
