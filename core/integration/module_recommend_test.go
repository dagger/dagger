package core

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestModuleRecommend(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExecRaw(ctx, t, workdir, "mod", "recommend", "--auto-apply")
	require.NoError(t, err, "%s", out)
	require.Contains(t, string(out), "No recommendations.")
	require.NoFileExists(t, filepath.Join(workdir, "dagger.toml"))

	require.NoError(t, os.WriteFile(filepath.Join(workdir, ".ruff.toml"), []byte("line-length = 88\n"), 0o644))
	for _, command := range []string{"mod", "module"} {
		out, err := hostDaggerExecRaw(ctx, t, workdir, command, "recommend")
		require.NoError(t, err, "%s", out)
		require.Contains(t, string(out), "No modules were installed. Run the commands for the modules you select.")
		require.Contains(t, string(out), "dagger module install dagger.io/python/ruff")
		require.NotContains(t, string(out), "Cloud account")
		require.NotContains(t, string(out), "Workspace migration")
		require.NotContains(t, string(out), "Setup complete.")
		require.NoFileExists(t, filepath.Join(workdir, "dagger.toml"))
	}
}
