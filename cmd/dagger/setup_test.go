package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupCommandMetadata(t *testing.T) {
	require.Equal(t, "setup", setupCmd.Use)
	require.Equal(t, []string{"migrate"}, setupCmd.Aliases)
	require.Contains(t, setupCmd.Short, "Ensure your Dagger workspace")
	require.False(t, setupCmd.Hidden)
}

func TestSetupHints(t *testing.T) {
	got := strings.Join(setupHints, "\n")
	for _, want := range []string{
		"dagger check",
		"dagger generate",
		"dagger functions",
		"dagger --web check",
		"https://docs.dagger.io/extending/",
	} {
		require.Contains(t, got, want)
	}
}

func TestDetectSetupSDK(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "go", file: "go.mod", want: "go"},
		{name: "typescript", file: "package.json", want: "typescript"},
		{name: "python", file: "pyproject.toml", want: "python"},
		{name: "unknown", file: "README.md", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.file), []byte("{}"), 0o600))
			require.Equal(t, tt.want, detectSetupSDK(dir))
		})
	}
}

func TestSetupRecommendationsFor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".markdownlint.yaml"), []byte("---\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger.json"), []byte(`{
  "name": "example",
  "dependencies": [
    {"name": "ruff", "source": "github.com/dagger/dagger/modules/ruff"}
  ]
}`), 0o600))

	recs, err := setupRecommendationsFor(dir)
	require.NoError(t, err)
	require.ElementsMatch(t, []setupRecommendation{
		{Name: "gha", Address: "github.com/dagger/dagger/modules/gha", Reason: ".github/workflows/"},
		{Name: "markdownlint", Address: "github.com/dagger/dagger/modules/markdownlint", Reason: ".markdownlint config"},
	}, recs)
}
