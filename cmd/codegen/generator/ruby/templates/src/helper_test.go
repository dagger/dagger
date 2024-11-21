package test

import (
	"flag"
	"os"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator/ruby/templates"
)

var updateFixtures = flag.Bool("test.update-fixtures", false, "update the test fixtures")

func updateAndGetFixtures(t *testing.T, filepath, got string) string {
	t.Helper()
	if *updateFixtures {
		err := os.WriteFile(filepath, []byte(got), 0o600)
		require.NoError(t, err)
	}
	want, err := os.ReadFile(filepath)
	require.NoError(t, err)

	return string(want)
}

func templateHelper(t *testing.T) *template.Template {
	t.Helper()
	return templates.New("")
}
