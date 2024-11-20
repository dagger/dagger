package typescriptgenerator

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"

	"github.com/psanford/memfs"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/ruby/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const ClientGenFile = "client.gen.rb"

type RubyGenerator struct {
	Config generator.Config
}

// Generate will generate the Ruby SDK code and might modify the schema to reorder types in a alphanumeric fashion.
func (g *RubyGenerator) Generate(_ context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
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

	tmpl := templates.New(schemaVersion)
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

	target := ClientGenFile
	if g.Config.ModuleName != "" {
		target = filepath.Join(g.Config.ModuleContextPath, "lib/dagger", ClientGenFile)
	}
	if err := mfs.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, err
	}
	if err := mfs.WriteFile(target, b.Bytes(), 0600); err != nil {
		return nil, err
	}

	return &generator.GeneratedState{
		Overlay: mfs,
	}, nil
}
