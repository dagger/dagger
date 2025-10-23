package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"go/format"
	"os"
	"path/filepath"
	"strings"
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
	funcs := GoTemplateFuncs(context.Background(), schema, "v0.0.0", generator.Config{}, nil, nil, 0)
	fullPaths := make([]string, len(paths))
	for i, path := range paths {
		fullPaths[i] = filepath.Join("src", path)
	}
	tmpl, err := template.New(filepath.Base(paths[0])).Funcs(funcs).ParseFiles(fullPaths...)
	require.NoError(t, err)
	return tmpl.Lookup(filepath.Base(paths[0]))
}

func renderTemplate(t *testing.T, tmpl *template.Template, data any) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, data))
	source := "package main\n\n" + buf.String()
	formatted, err := format.Source([]byte(source))
	require.NoError(t, err)
	return strings.TrimPrefix(string(formatted), "package main\n\n")
}

func updateAndGetFixture(t *testing.T, path string, got string) string {
	t.Helper()
	fullPath := resolveFixturePath(path)
	if *updateFixtures {
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte(got), 0o600))
	}
	want, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	return string(want)
}

func resolveFixturePath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return filepath.Join("cmd/codegen/generator/go/templates", path)
}
