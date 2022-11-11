package test

import (
	"bytes"
	"context"
	_ "embed"
	"sort"
	"testing"

	"dagger.io/dagger"
	generator "github.com/dagger/dagger/codegen/generator/go"
	"github.com/stretchr/testify/require"
)

//nolint:typecheck
//go:embed testdata/want-test-full.ts
var wantTestFull string

func TestFull(t *testing.T) {
	tmpl := templateHelper(t, "header", "objects", "object", "method", "method_solve", "field", "return_solve", "input_args", "return", "object_comment", "field_comment", "types", "type")

	want := wantTestFull
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)

	schema, err := generator.Introspect(ctx, c)
	require.NoError(t, err)

	generator.SetSchemaParents(schema)

	sort.SliceStable(schema.Types, func(i, j int) bool {
		return schema.Types[i].Name < schema.Types[j].Name
	})
	for _, v := range schema.Types {
		sort.SliceStable(v.Fields, func(i, j int) bool {
			return v.Fields[i].Name < v.Fields[j].Name
		})
	}

	var b bytes.Buffer

	err = tmpl.ExecuteTemplate(&b, "objects", schema.Types)
	require.NoError(t, err)

	// 	err = os.WriteFile("./testdata/want-test-full.ts", b.Bytes(), 0o644)
	// 	require.NoError(t, err)

	require.Equal(t, want, b.String())
}
