package gogenerator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestGenerateClientRemovesStaleDependencyBindings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		clientDir string
	}{
		{name: "root", clientDir: "."},
		{name: "subdirectory", clientDir: "sdk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(outDir, "go.mod"),
				[]byte("module example.com/client\n\ngo 1.25.0\n"),
				0o600,
			))
			if tc.clientDir == "." {
				require.NoError(t, os.WriteFile(filepath.Join(outDir, "client.go"), []byte("package main\n"), 0o600))
			}

			stalePath := filepath.Join(tc.clientDir, "old-dependency.gen.go")
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(outDir, stalePath)), 0o755))
			require.NoError(t, os.WriteFile(
				filepath.Join(outDir, stalePath),
				[]byte(daggerGeneratedHeader+"\n\npackage dagger\n"),
				0o600,
			))

			g := GoGenerator{Config: generator.Config{
				OutputDir: outDir,
				ClientConfig: &generator.ClientGeneratorConfig{
					ModuleName: "client",
					ClientDir:  tc.clientDir,
				},
			}}

			state, err := g.GenerateClient(t.Context(), clientTestSchema(), "v0.18.0")
			require.NoError(t, err)
			require.Equal(t, []string{stalePath}, state.RemovePaths)
		})
	}
}

func clientTestSchema() *introspection.Schema {
	schema := &introspection.Schema{
		Types: introspection.Types{
			{Kind: introspection.TypeKindObject, Name: "Query"},
			{Kind: introspection.TypeKindScalar, Name: "Boolean"},
			{Kind: introspection.TypeKindScalar, Name: "Float"},
			{Kind: introspection.TypeKindScalar, Name: "ID"},
			{Kind: introspection.TypeKindScalar, Name: "Int"},
			{Kind: introspection.TypeKindScalar, Name: "String"},
		},
	}
	schema.QueryType.Name = "Query"
	return schema
}
