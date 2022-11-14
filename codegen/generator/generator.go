package generator

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/introspection"
)

var ErrUnknownSDKLang = errors.New("unknown sdk language")

type SDKLang string

const (
	SDKLangGo     SDKLang = "go"
	SDKLangNodeJS SDKLang = "nodejs"
	SDKLangPython SDKLang = "python"
)

type Config struct {
	Lang SDKLang
	// Package is the target package that is generated.
	// Not used for the SDKLangNodeJS.
	Package string
}

type Generator interface {
	Generate(ctx context.Context, schema *introspection.Schema) ([]byte, error)
}

// SetSchemaParents sets all the parents for the fields.
func SetSchemaParents(schema *introspection.Schema) {
	for _, t := range schema.Types {
		for _, f := range t.Fields {
			f.ParentObject = t
		}
	}
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
func IntrospectAndGenerate(ctx context.Context, c *dagger.Client, generator Generator) ([]byte, error) {
	schema, err := Introspect(ctx, c)
	if err != nil {
		return nil, err
	}

	return generator.Generate(ctx, schema)
}
