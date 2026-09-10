package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceConfigPreservation(ctx context.Context, t *testctx.T) {
	const config = `# Keep this layout
ignore = [
    'dist', # generated output
    "node_modules",
]
future = { enabled = true }

[modules.go-sdk]
source = 'github.com/dagger/go-sdk'
entrypoint = false # keep this comment

[modules.go-sdk.as-sdk]
name = 'go'

[sdks.go]
module = 'go-sdk'

[sdks.go.scopes."apps/api"]
name = 'api' # scope notes
clients = [
    './database',
]
`
	for _, tc := range []struct {
		name          string
		args          []string
		before, after string
	}{
		{"config set", []string{"ws", "config", "modules.go-sdk.entrypoint", "true"}, "entrypoint = false", "entrypoint = true"},
		{"config unset", []string{"ws", "config", "modules.go-sdk.entrypoint", "--unset"}, "entrypoint = false # keep this comment\n", ""},
		{"SDK scope", []string{"sdk", "scope", "--path=apps/api", "name", "service"}, "name = 'api'", "name = 'service'"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := newWorkspaceConfigWorkdir(ctx, t, config)
			_, err := hostDaggerExec(ctx, t, workdir, tc.args...)
			require.NoError(t, err)
			contents, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
			require.NoError(t, err)
			require.Equal(t, strings.Replace(config, tc.before, tc.after, 1), string(contents))
		})
	}

	t.Run("uninstall preserves other modules", func(ctx context.Context, t *testctx.T) {
		const extra = "\n[modules.extra]\nsource = './extra'\nfuture = 'remove with module'\n"
		workdir := newWorkspaceConfigWorkdir(ctx, t, config+extra)
		_, err := hostDaggerExec(ctx, t, workdir, "mod", "uninstall", "extra")
		require.NoError(t, err)
		contents, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Equal(t, config+"\n", string(contents))
	})
}
