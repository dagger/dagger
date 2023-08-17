package generator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/graphql"
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

// Introspect get the Dagger Schema
func Introspect(ctx context.Context) (*introspection.Schema, error) {
	api, err := schema.New(schema.InitializeArgs{})
	if err != nil {
		return nil, err
	}
	apiSchema := api.Schema()
	resp := graphql.Do(graphql.Params{Schema: *apiSchema, RequestString: introspection.Query, Context: ctx})
	if resp.Errors != nil {
		errs := make([]error, len(resp.Errors))
		for i, err := range resp.Errors {
			errs[i] = err
		}
		return nil, errors.Join(errs...)
	}

	var introspectionResp introspection.Response
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}
	err = json.Unmarshal(dataBytes, &introspectionResp)
	if err != nil {
		return nil, fmt.Errorf("unmarshal data: %w", err)
	}
	return introspectionResp.Schema, nil
}

// IntrospectAndGenerate generate the Dagger API
func IntrospectAndGenerate(ctx context.Context, generator Generator) ([]byte, error) {
	schema, err := Introspect(ctx)
	if err != nil {
		return nil, err
	}

	return generator.Generate(ctx, schema)
}
