package gogenerator

import (
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

func TestSyncModReplaceAndTidyPinsDaggerWithoutUpdatingTransitiveDeps(t *testing.T) {
	t.Parallel()

	mod, err := modfile.Parse("go.mod", []byte("module example.com/test\n\ngo 1.25.0\n"), nil)
	require.NoError(t, err)

	genSt := &generator.GeneratedState{}
	g := GoGenerator{
		Config: generator.Config{
			OutputDir: t.TempDir(),
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName:       "test",
				ModuleSourcePath: ".",
				LibVersion:       "v1.2.3",
			},
		},
	}

	require.NoError(t, g.syncModReplaceAndTidy(mod, genSt, "."))

	var goGetArgs []string
	for _, cmd := range genSt.PostCommands {
		if len(cmd.Args) >= 2 && cmd.Args[0] == "go" && cmd.Args[1] == "get" {
			goGetArgs = cmd.Args
			break
		}
	}

	require.Equal(t, []string{"go", "get", "dagger.io/dagger@v1.2.3"}, goGetArgs)
}

func TestSyncModReplaceAndTidyPinsGeneratedModuleImports(t *testing.T) {
	t.Parallel()

	mod, err := modfile.Parse("go.mod", []byte("module example.com/test\n\ngo 1.25.0\n"), nil)
	require.NoError(t, err)

	g := GoGenerator{
		Config: generator.Config{
			OutputDir: t.TempDir(),
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName:       "test",
				ModuleSourcePath: ".",
				LibVersion:       "v1.2.3",
			},
		},
	}

	require.NoError(t, g.syncModReplaceAndTidy(mod, &generator.GeneratedState{}, "."))

	required := map[string]string{}
	for _, req := range mod.Require {
		required[req.Mod.Path] = req.Mod.Version
	}
	// Generated module code imports these, but dagger.io/dagger does not.
	// Unpinned, `go mod tidy` picks their latest versions, and otel/sdk v1.47.0
	// raises otel to a version that cycles with the copied otel/log replace.
	for _, path := range []string{
		"github.com/dagger/otel-go",
		"go.opentelemetry.io/otel/sdk",
	} {
		require.Contains(t, required, path)
	}
}
