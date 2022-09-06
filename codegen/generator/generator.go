package generator

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"strings"

	"go.dagger.io/dagger/codegen/generator/templates"
	"go.dagger.io/dagger/codegen/introspection"
)

type Config struct {
	Package string
}

func Generate(ctx context.Context, schema *introspection.Schema, cfg Config) ([]byte, error) {
	gen := &GoGenerator{
		cfg:    cfg,
		schema: schema,
	}
	return gen.Generate(ctx)
}

type GoGenerator struct {
	cfg    Config
	schema *introspection.Schema
}

func (g *GoGenerator) Generate(_ context.Context) ([]byte, error) {
	headerData := struct {
		Package string
		Schema  *introspection.Schema
	}{
		Package: g.cfg.Package,
		Schema:  g.schema,
	}
	var header bytes.Buffer
	if err := templates.Header.Execute(&header, headerData); err != nil {
		return nil, err
	}

	render := []string{
		header.String(),
	}

	// indented, err := json.MarshalIndent(g.schema, "", "  ")
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Fprint(os.Stdout, string(indented))

	err := g.schema.Visit(introspection.VisitHandlers{
		Scalar: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Scalar.Execute(&out, t); err != nil {
				return err
			}
			render = append(render, out.String())
			return nil
		},
		Object: func(t *introspection.Type) error {
			var out bytes.Buffer
			if err := templates.Object.Execute(&out, t); err != nil {
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
		// FIXME: temporary limit on the generation
		Allowed: map[string]struct{}{
			// Scalars
			"DirectoryID":      {},
			"SecretID":         {},
			"ContainerID":      {},
			"FileID":           {},
			"ContainerAddress": {},

			// Inputs
			"ExecOpts": {},

			// Objects
			"Query":         {},
			"File":          {},
			"Directory":     {},
			"Container":     {},
			"GitRepository": {},
			"GitRef":        {},
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
