package typescriptgenerator

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/psanford/memfs"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const ClientGenFile = "client.gen.ts"

type TypeScriptGenerator struct {
	Config generator.Config
}

// Generate will generate the TypeScript SDK code and might modify the schema to reorder types in a alphanumeric fashion.
func (g *TypeScriptGenerator) GenerateModule(_ context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	target := filepath.Join(g.Config.ModuleConfig.ModuleSourcePath, "sdk/src/api", ClientGenFile)

	return generate(g.Config, target, schema, schemaVersion)
}

func (g *TypeScriptGenerator) GenerateClient(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	return generate(g.Config, ClientGenFile, schema, schemaVersion)
}

func (g *TypeScriptGenerator) GenerateLibrary(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	return generate(g.Config, ClientGenFile, schema, schemaVersion)
}

func generate(config generator.Config, target string, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	sort.SliceStable(schema.Types, func(i, j int) bool {
		return schema.Types[i].Name < schema.Types[j].Name
	})
	for _, v := range schema.Types {
		sort.SliceStable(v.Fields, func(i, j int) bool {
			in := v.Fields[i].Name
			jn := v.Fields[j].Name
			switch {
			case in == "id" && jn == "id":
				return false
			case in == "id":
				return true
			case jn == "id":
				return false
			default:
				return in < jn
			}
		})
	}

	tmpl := templates.New(schemaVersion, config)
	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
	}{
		Schema:        schema,
		SchemaVersion: schemaVersion,
		Types:         schema.Types,
	}
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "api", data)
	if err != nil {
		return nil, err
	}

	mfs := memfs.New()

	if err := mfs.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, fmt.Errorf("failed to create target directory %s: %w", filepath.Dir(target), err)
	}
	if err := mfs.WriteFile(target, b.Bytes(), 0600); err != nil {
		return nil, fmt.Errorf("failed to write client file at %s: %w", target, err)
	}

	return &generator.GeneratedState{
		Overlay: mfs,
	}, nil
}

func (g *TypeScriptGenerator) GenerateTypeDefs(_ context.Context) (*generator.GeneratedState, error) {
	return nil, fmt.Errorf("not implemented for %s SDK", generator.SDKLangTypeScript)
}
