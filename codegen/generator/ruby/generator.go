package nodegenerator

import (
	"bytes"
	"context"
	"sort"

	"github.com/dagger/dagger/codegen/generator/ruby/templates"
	"github.com/dagger/dagger/codegen/introspection"
)

type RubyGenerator struct{}

// Generate will generate the Ruby SDK code and might modify the schema to reorder types in a alphanumeric fashion.
func (g *RubyGenerator) Generate(_ context.Context, schema *introspection.Schema) ([]byte, error) {
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

	return b.Bytes(), err
}
