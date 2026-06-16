package templates

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestNewGoSDKInterfaceSurface(t *testing.T) {
	idRef := &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "ID",
		},
	}
	id := &introspection.Type{Kind: introspection.TypeKindScalar, Name: "ID"}
	query := &introspection.Type{Kind: introspection.TypeKindObject, Name: "Query"}
	iface := &introspection.Type{
		Kind: introspection.TypeKindInterface,
		Name: "DepCustomIface",
		Fields: []*introspection.Field{{
			Name:    "id",
			TypeRef: idRef,
		}},
	}
	schema := &introspection.Schema{Types: introspection.Types{id, query, iface}}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })

	funcs := GoTemplateFuncs(t.Context(), schema, schema, "v0.21.0-dev", generator.Config{
		ModuleConfig: &generator.ModuleGeneratorConfig{ModuleName: "test"},
	}, nil, nil, 0)
	tree, err := buildTemplateTree(funcs)
	require.NoError(t, err)
	tmpl := tree.Lookup("internal/dagger/dagger.gen.go.tmpl")
	require.NotNil(t, tmpl)

	data := struct {
		Schema        *introspection.Schema
		SchemaVersion string
		Types         []*introspection.Type
	}{
		Schema:        schema,
		SchemaVersion: "v0.21.0-dev",
		Types:         schema.Visit(),
	}

	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, data))
	got := buf.String()

	require.Contains(t, got, "type DepCustomIface interface")
	require.Contains(t, got, "type DepCustomIfaceClient struct")
	require.NotContains(t, got, "type DepCustomIfaceID = ID")
	require.NotContains(t, got, "LoadDepCustomIfaceFromID")
}
