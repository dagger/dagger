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

	funcs := templates.GoTemplateFuncs(g.Config.ModuleName, g.Config.SourceDirectoryPath, schema)

	headerData := struct {
		Package string
		Schema  *introspection.Schema
	}{
		Package: g.Config.Package,
		Schema:  schema,
	}

	var render []string

	var header bytes.Buffer
	if err := templates.Header(funcs).Execute(&header, headerData); err != nil {
		return nil, err
	}
	render = append(render, header.String())

	err := schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Scalar(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			objectTmpl := templates.Object(funcs)

			// don't create methods on query for the env itself, only its deps
			// e.g. don't create `func (r *DAG) Go() *Go` in the Go env's codegen
			if g.Config.ModuleName != "" && t.Name == generator.QueryStructName {
				var newFields []*introspection.Field
				for _, f := range t.Fields {
					if f.Name != g.Config.ModuleName {
						newFields = append(newFields, f)
					}
				}
				t.Fields = newFields
			}

			objectName := strings.ToLower(t.Name[:1]) + t.Name[1:]
			if g.Config.ModuleName == objectName {
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
			if err := templates.Enum(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Input: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Input(funcs).Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	if g.Config.ModuleName != "" {
		moduleData := struct {
			Schema              *introspection.Schema
			SourceDirectoryPath string
		}{
			Schema:              schema,
			SourceDirectoryPath: g.Config.SourceDirectoryPath,
		}

		var moduleMain bytes.Buffer
		if err := templates.Module(funcs).Execute(&moduleMain, moduleData); err != nil {
			return nil, err
		}
		render = append(render, moduleMain.String())
	}

	formatted, err := format.Source(
		[]byte(strings.Join(render, "\n")),
	)
	if err != nil {
		return nil, fmt.Errorf("error formatting generated code: %w", err)
	}
	return formatted, nil
}
