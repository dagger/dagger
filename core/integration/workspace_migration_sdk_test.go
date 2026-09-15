package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceMigrationSuite) TestWorkspaceMigrateInstalledSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, source := range []string{"github.com/dagger/dang-sdk@v0.1", "dagger.io/sdk/dang@v1"} {
		t.Run(source, func(ctx context.Context, t *testctx.T) {
			original := fmt.Sprintf(`[modules.app]
source = './app'
[modules.provider]
source = %q
[modules.provider.as-sdk]
name = 'dang'
[[modules.provider.as-sdk.modules]]
path = 'app'
`, source)
			module := "name = 'app'\n[runtime]\nsource = 'dang'\n"
			base := workspaceBase(t, c).
				WithNewFile("dagger.toml", original).
				WithNewFile("app/dagger-module.toml", module)
			preview := base.With(daggerExec("ws", "migrate", "--no-apply"))
			unchanged, err := preview.File("dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, original, unchanged)

			applied := preview.With(daggerExec("ws", "migrate", "--auto-apply"))
			updated, err := applied.File("dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, updated, "as-sdk")
			cfg, err := workspace.ParseConfig([]byte(updated))
			require.NoError(t, err)
			require.Len(t, cfg.Modules, 2)
			require.Len(t, cfg.SDKs, 1)
			require.Equal(t, source, cfg.Modules["provider"].Source)
			require.Equal(t, "provider", cfg.SDKs["dang"].Module)
			require.True(t, cfg.SDKs["dang"].Scopes["app"].IsModule)
			unchanged, err = applied.File("app/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, module, unchanged)

			again := applied.With(daggerExec("ws", "migrate", "--auto-apply"))
			repeated, err := again.File("dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, updated, repeated)
		})
	}
}
