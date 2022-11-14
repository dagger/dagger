package test

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

func TestAPI(t *testing.T) {
	tmpl := templateHelper(t)

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

	err = tmpl.ExecuteTemplate(&b, "api", schema.Types)
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", "want-api-full.golden.ts")
	if *update {
		err = os.WriteFile(goldenPath, b.Bytes(), 0o600)
		require.NoError(t, err)
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)

	require.Equal(t, string(want), b.String())
}
