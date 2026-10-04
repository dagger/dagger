package templates

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	dagger "dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestModuleSemanticConventionsMatchSDK(t *testing.T) {
	template, err := tmplFS.ReadFile("src/_dagger.gen.go/module.go.tmpl")
	require.NoError(t, err)
	match := regexp.MustCompile(`semconv "([^"]+)"`).FindSubmatch(template)
	require.Len(t, match, 2)

	// Resolve the generated import against the SDK's own dependency versions.
	// A newer semconv import must not silently upgrade OTel during generation:
	// that can conflict with the SDK's pinned pre-1.0 log packages.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), dagger.GoMod, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), dagger.GoSum, 0o600))
	cmd := exec.CommandContext(t.Context(), "go", "list", "-mod=readonly", string(match[1]))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
