package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestMainChangePreservesSDKModuleName(t *testing.T) {
	for _, next := range []string{"", "other"} {
		t.Run("main="+next, func(t *testing.T) {
			cfg := &workspace.Config{SDKs: map[string]workspace.SDKEntry{"test": {Scopes: map[string]workspace.SDKScope{"generated/api": {IsModule: true}}}}, Modules: map[string]workspace.ModuleEntry{
				"demo-dev": {Source: "generated/api", Main: true},
				"other":    {Source: "other"},
			}}
			ws := &core.Workspace{Cwd: ".", ConfigFile: "dagger.toml"}
			before, err := resolveSDKModuleName(ws, cfg, ".", "generated/api", "")
			require.NoError(t, err)
			require.Equal(t, "demo-dev", before)
			require.NoError(t, workspace.SetMain(cfg, ".", next))
			after, err := resolveSDKModuleName(ws, cfg, ".", "generated/api", cfg.SDKs["test"].Scopes["generated/api"].Name)
			require.NoError(t, err)
			require.Equal(t, before, after, "changing the main must not rename an existing SDK module")
		})
	}
}
