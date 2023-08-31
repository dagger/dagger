package generator

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
)

var ErrUnknownSDKLang = errors.New("unknown sdk language")

// TODO: de-dupe this with environment api
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
	// TODO:
	EnvironmentName        string
	DependencyEnvironments []*dagger.Environment
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
func Introspect(ctx context.Context, engineClient *client.Client) (*introspection.Schema, error) {
	if engineClient == nil {
		var err error
		engineClient, ctx, err = client.Connect(ctx, client.Params{
			RunnerHost: engine.RunnerHost(),
		})
		if err != nil {
			return nil, err
		}
		defer engineClient.Close()
	}

	var introspectionResp introspection.Response
	err := engineClient.Do(ctx, introspection.Query, "IntrospectionQuery", nil, &introspectionResp)
	if err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}

	return introspectionResp.Schema, nil
}
