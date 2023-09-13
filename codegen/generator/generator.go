package generator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
)

var ErrUnknownSDKLang = errors.New("unknown sdk language")

// TODO: de-dupe this with moduleconfig api
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

	ModuleName          string
	DependencyModules   []*dagger.Module
	SourceDirectoryPath string
}

type Generator interface {
	// Generate runs codegen and returns a map of default filename to content for that file.
	Generate(ctx context.Context, schema *introspection.Schema) (*GeneratedState, error)
}

type GeneratedState struct {
	// Overlay is the overlay filesystem that contains generated code to write
	// over the output directory.
	Overlay fs.FS

	// PostCommands are commands that need to be run after the codegen has
	// finished. This is used for example to run `go mod tidy` after generating
	// Go code.
	PostCommands []*exec.Cmd

	// NeedSync indicates that the code needs to be generated again. This can
	// happen if the codegen spat out templates that depend on generated types.
	// In that case the codegen needs to be run again with both the templates and
	// the initially generated types available.
	NeedRegenerate bool
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
