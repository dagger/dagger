package generator

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/router"
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

// Introspect get the Dagger Schema with the router r.
func Introspect(ctx context.Context, r *router.Router) (*introspection.Schema, error) {
	var response introspection.Response
	_, err := r.Do(ctx, introspection.Query, "", nil, &response)
	if err != nil {
		return nil, fmt.Errorf("error querying the API: %w", err)
	}
	return response.Schema, nil
}

// IntrospectAndGenerate generate the Dagger API with the router r.
func IntrospectAndGenerate(ctx context.Context, r *router.Router, generator Generator) ([]byte, error) {
	schema, err := Introspect(ctx, r)
	if err != nil {
		return nil, err
	}

	return generator.Generate(ctx, schema)
}
