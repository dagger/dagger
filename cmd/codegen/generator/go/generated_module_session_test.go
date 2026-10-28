package gogenerator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/go/templates"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/stretchr/testify/require"
)

// Compile and execute both generated packages without an inherited session.
// A source-only assertion would miss an eager initializer in either package,
// or a global author handle that permanently remembers missing configuration.
func TestGeneratedModuleSessionIsLazy(t *testing.T) {
	root := t.TempDir()
	data, err := os.ReadFile("../../introspection/testdata/schema.json")
	require.NoError(t, err)
	var schema introspection.Schema
	require.NoError(t, json.Unmarshal(data, &schema))
	generator.SetSchemaParents(&schema)
	previous := generator.GetSchema()
	generator.SetSchema(&schema)
	t.Cleanup(func() { generator.SetSchema(previous) })
	cfg := generator.Config{OutputDir: root, ModuleConfig: &generator.ModuleGeneratorConfig{ModuleName: "example", ModuleSourcePath: "."}}
	funcs := templates.GoTemplateFuncs(t.Context(), &schema, &schema, "v1.0.0-beta.21", cfg, nil, nil, 0)
	tmpls := templates.Templates(funcs)
	pkg := &PackageInfo{PackageName: "main", PackageImport: "example.com/importsafe"}
	for _, name := range []string{"internal/dagger/core/core.gen.go", "internal/dagger/dagger.gen.go"} {
		source, err := renderFile(root, &schema, "v1.0.0-beta.21", pkg, tmpls[name])
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), source, 0600))
	}
	// Reuse the SDK's dependency pins, including its telemetry replacements.
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("../../../../sdk/go", name))
		require.NoError(t, err)
		if name == "go.mod" {
			data = []byte(strings.Replace(string(data), "module dagger.io/dagger", "module example.com/importsafe", 1))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0600))
	}
	testSource, err := os.ReadFile("testdata/module-session/session_test.go")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "session_test.go"), testSource, 0600))
	cmd := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
	cmd.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DAGGER_SESSION_PORT=") && !strings.HasPrefix(entry, "DAGGER_SESSION_TOKEN=") && !strings.HasPrefix(entry, "GOWORK=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOWORK=off")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
