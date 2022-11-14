package generator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/format"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator/go/templates"
	"github.com/dagger/dagger/codegen/introspection"
)

var ErrUnknownSDK = errors.New("unknown sdk language")

type SDKLang string

const (
	SDKLangUnknown SDKLang = ""
	SDKLangGo      SDKLang = "go"
	SDKLangNodeJS  SDKLang = "nodejs"
	SDKLangPython  SDKLang = "python"
)

type Config struct {
	Lang    SDKLang
	Package string
}

type Generator interface {
	Generate(ctx context.Context) ([]byte, error)
}

// SetSchemaParents sets all the parents for the fields.
func SetSchemaParents(schema *introspection.Schema) {
	for _, t := range schema.Types {
		for _, f := range t.Fields {
			f.ParentObject = t
		}
	}
}

func Generate(ctx context.Context, schema *introspection.Schema, cfg Config) ([]byte, error) {
	SetSchemaParents(schema)

	var gen Generator
	switch cfg.Lang {
	case SDKLangGo:
		gen = &GoGenerator{
			cfg:    cfg,
			schema: schema,
		}

	default:
		sdks := []string{
			string(SDKLangGo),
		}
		return []byte{}, fmt.Errorf("use SDK: [%s]: %w", strings.Join(sdks, ", "), ErrUnknownSDK)
	}

	return gen.Generate(ctx)
}

// Introspect get the Dagger Schema with the client c.
func Introspect(ctx context.Context, c *dagger.Client) (*introspection.Schema, error) {
	var response introspection.Response
	err := c.Do(ctx,
		&dagger.Request{
			Query: introspection.Query,
		},
		&dagger.Response{Data: &response},
	)
	if err != nil {
		return nil, fmt.Errorf("error querying the API: %w", err)
	}
	return response.Schema, nil
}

// IntrospectAndGenerate generate the Dagger API with the client c.
func IntrospectAndGenerate(ctx context.Context, c *dagger.Client, cfg Config) ([]byte, error) {
	schema, err := Introspect(ctx, c)
	if err != nil {
		return nil, err
	}

	return Generate(ctx, schema, cfg)
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
