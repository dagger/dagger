package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"go/format"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

var updateFixtures = flag.Bool("test.update-fixtures", false, "update the test fixtures")

func loadSchemaFromTypeJSON(t *testing.T, jsonString string) (*introspection.Schema, *introspection.Type) {
	t.Helper()
	var object introspection.Type
	require.NoError(t, json.Unmarshal([]byte(jsonString), &object))
	schema := &introspection.Schema{
		Types: introspection.Types{&object},
	}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	return schema, &object
}

func parseTemplateFiles(t *testing.T, schema *introspection.Schema, paths ...string) *template.Template {
	t.Helper()
	if len(paths) == 0 {
		t.Fatalf("parseTemplateFiles requires at least one template path")
	}
	funcs := GoTemplateFuncs(t.Context(), schema, "v0.0.0", generator.Config{}, nil, nil, 0)
	fullPaths := make([]string, len(paths))
	for i, path := range paths {
		fullPaths[i] = filepath.Join("src", path)
	}
	root := filepath.Base(paths[0])
	tmpl, err := template.New(root).Funcs(funcs).ParseFiles(fullPaths...)
	require.NoErrorf(t, err, "parse template %q", paths)
	lookup := tmpl.Lookup(root)
	require.NotNilf(t, lookup, "template %q not found after parsing %q", root, paths)
	return lookup
}

func renderTemplate(t *testing.T, tmpl *template.Template, data any) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, data))
	source := "package main\n\n" + buf.String()
	formatted, err := format.Source([]byte(source))
	require.NoErrorf(t, err, "gofmt template %q failed:\n%s", tmpl.Name(), buf.String())
	return string(bytes.TrimPrefix(formatted, []byte("package main\n\n")))
}

func updateAndGetFixture(t *testing.T, path string, got string) string {
	t.Helper()
	fullPath := resolveFixturePath(path)
	if *updateFixtures {
		require.NoErrorf(t, os.MkdirAll(filepath.Dir(fullPath), 0o755), "create fixture dir %q", filepath.Dir(fullPath))
		require.NoErrorf(t, os.WriteFile(fullPath, []byte(got), 0o644), "write fixture %q", fullPath)
	}
	want, err := os.ReadFile(fullPath)
	require.NoErrorf(t, err, "read fixture %q", fullPath)
	return string(want)
}

func resolveFixturePath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	} else if !errors.Is(err, os.ErrNotExist) {
		return path
	}
	return filepath.Join("cmd/codegen/generator/go/templates", path)
}
