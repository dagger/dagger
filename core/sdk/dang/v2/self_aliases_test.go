package dangv2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestModuleSelfAliases(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte("type Original {}\ntype Widget { value: String! }\n"), 0600))
	require.Equal(t, map[string]string{"original": "renamed", "Original": "Renamed", "OriginalWidget": "RenamedWidget"}, moduleSelfAliases(&core.ModuleSource{ModuleName: "renamed", ModuleOriginalName: "original"}, dir))
	require.Nil(t, moduleSelfAliases(&core.ModuleSource{ModuleName: "original", ModuleOriginalName: "original"}, dir))
}
