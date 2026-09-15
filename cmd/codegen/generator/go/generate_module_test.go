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
