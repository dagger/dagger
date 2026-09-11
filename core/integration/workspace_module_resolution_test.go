package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceModuleResolution(ctx context.Context, t *testctx.T) {
	t.Run("remote errors retain the requested source", func(ctx context.Context, t *testctx.T) {
		for _, source := range []string{
			"github.com/does/notexist@v1",
			"https://github.com/dagger/dagger#this-version-does-not-exist:modules/wolfi",
			"http://127.0.0.1:1/repo.git#main",
		} {
			workdir := newWorkspaceConfigWorkdir(ctx, t, "")
			out, err := hostDaggerExec(ctx, t, workdir, "mod", "install", source)
			require.Error(t, err)
			require.Contains(t, string(out), source)
			require.NotContains(t, string(out), "local path does not exist")
			require.NotContains(t, string(out), "➡️")
			if strings.HasPrefix(source, "http://127.0.0.1:") {
				require.Regexp(t, "connection refused|cannot connect to git repository", string(out))
			}
		}
	})

	t.Run("explicit local error stays local", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, "")
		out, err := hostDaggerExec(ctx, t, workdir, "mod", "install", "./github.com/does/notexist@v1")
		require.Error(t, err)
		require.Contains(t, string(out), "local")
		require.NotContains(t, string(out), "➡️")
	})

	t.Run("locked vanity source reports once on stderr", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, "")
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, workspace.LockOperationVanityURL,
			[]any{"https://go.example/tools"}, "https://github.com/dagger/dagger/modules/wolfi"))
		data, err := lock.Marshal()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(workdir, workspace.LockFileName), data, 0o644))
		const source = "go.example/tools@v0.20.2"
		const resolution = source + " ➡️ https://github.com/dagger/dagger#v0.20.2:modules/wolfi"
		for range 2 {
			cmd := hostDaggerCommand(ctx, t, workdir, "mod", "install", source, "--name=wolfi")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			require.NoError(t, err, "%s", stderr.String())
			require.NotContains(t, string(stdout), "➡️")
			require.Equal(t, 1, strings.Count(stderr.String(), resolution), stderr.String())
			require.Equal(t, 1, strings.Count(stderr.String(), "➡️"), stderr.String())
		}
	})
}
