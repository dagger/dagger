package nodegenerator

import (
	"bytes"
	"context"
	"sort"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/generator/nodejs/templates"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/psanford/memfs"
)

const ClientGenFile = "client.gen.ts"

type NodeGenerator struct {
	Config generator.Config
}

// Generate will generate the NodeJS SDK code and might modify the schema to reorder types in a alphanumeric fashion.
func (g *NodeGenerator) Generate(_ context.Context, schema *introspection.Schema) (*generator.GeneratedState, error) {
	generator.SetSchema(schema)

	sort.SliceStable(schema.Types, func(i, j int) bool {
		return schema.Types[i].Name < schema.Types[j].Name
	})
	for _, v := range schema.Types {
		sort.SliceStable(v.Fields, func(i, j int) bool {
			return v.Fields[i].Name < v.Fields[j].Name
		})
	}

	tmpl := templates.New()
	var b bytes.Buffer
	err := tmpl.ExecuteTemplate(&b, "api", schema.Types)
	if err != nil {
		return nil, err
	}

	mfs := memfs.New()

	if err := mfs.WriteFile(ClientGenFile, b.Bytes(), 0600); err != nil {
		return nil, err
	}

	if err := generator.InstallGitAttributes(mfs, ClientGenFile, g.Config.SourceDirectoryPath); err != nil {
		return nil, err
	}

	return &generator.GeneratedState{
		Overlay: mfs,
	}, nil
}
