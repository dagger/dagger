package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceModulesSuite) TestWorkspaceModuleSelection(ctx context.Context, t *testctx.T) {
	const config = `# Keep the layout
ignore = [
  'dist',
]

[modules.tools]
source = 'github.com/does/notexist/tools@v1'

[modules.local]
source = './local'

[modules.default]
source = 'github.com/does/notexist/default'

[env.dev.modules.tools]
source = 'github.com/does/notexist/tools@v2'
`
	for _, tc := range []struct {
		name, selector, version, match string
	}{
		{name: "installed name", selector: "tools", version: "v1"},
		{name: "source", selector: "https://github.com/does/notexist/tools", version: "v1", match: `Matched installed module "tools" by source "github.com/does/notexist/tools".`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := newWorkspaceConfigWorkdir(ctx, t, config)
			cmd := hostDaggerCommandRaw(ctx, t, workdir, "mod", "version", tc.selector)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			require.NoError(t, err, stderr.String())
			require.Equal(t, tc.version+"\n", string(out))
			if tc.match != "" {
				require.Contains(t, stderr.String(), tc.match)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		err  string
	}{
		{"version local", []string{"mod", "version", "local"}, "has a local source"},
		{"version default", []string{"mod", "version", "default"}, "has no explicit version request"},
		{"version suffix", []string{"mod", "version", "tools@v1"}, "version selector is not allowed"},
		{"uninstall suffix", []string{"uninstall", "github.com/does/notexist/tools@v1"}, "version selector is not allowed"},
		{"missing installation", []string{"mod", "version", "missing"}, `module "missing" is not installed`},
		{"local update", []string{"mod", "update", "local", "--version=v2"}, "local module source"},
		{"update all version", []string{"update", "--version=v2"}, "--version requires exactly one"},
		{"conflicting version", []string{"update", "tools@v2", "--version=v3"}, "use either a version suffix or --version"},
		{"reinstall version", []string{"install", "github.com/does/notexist/tools@v2", "--name=tools"}, "use dagger mod update tools --version VERSION"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := newWorkspaceConfigWorkdir(ctx, t, config)
			out, err := hostDaggerExecRaw(ctx, t, workdir, tc.args...)
			require.Error(t, err)
			require.Contains(t, string(out), tc.err)
			data, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
			require.NoError(t, err)
			require.Equal(t, config, string(data))
		})
	}

	t.Run("reinstall is unchanged without fetching", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, config)
		out, err := hostDaggerExecRaw(ctx, t, workdir, "install", "https://github.com/does/notexist/tools@v1")
		require.NoError(t, err)
		require.Contains(t, string(out), `Module "tools" is already installed`)
		data, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Equal(t, config, string(data))
		_, err = os.Stat(filepath.Join(workdir, workspace.LockFileName))
		require.True(t, os.IsNotExist(err))
	})
	t.Run("uninstall by source", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, config)
		out, err := hostDaggerExecRaw(ctx, t, workdir, "mod", "uninstall", "https://github.com/does/notexist/tools")
		require.NoError(t, err)
		require.Contains(t, string(out), `Matched installed module "tools" by source "github.com/does/notexist/tools".`)
		data, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Equal(t, strings.Replace(config, "[modules.tools]\nsource = 'github.com/does/notexist/tools@v1'\n", "", 1), string(data))
	})
	t.Run("source version does not select one of two installations", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, config+"\n[modules.older]\nsource = 'github.com/does/notexist/tools@v0'\n")
		out, err := hostDaggerExecRaw(ctx, t, workdir, "mod", "update", "github.com/does/notexist/tools@v1")
		require.Error(t, err)
		require.Contains(t, string(out), `source "github.com/does/notexist/tools" matches installed modules "older", "tools"; use an installed name`)
	})
}

func (WorkspaceModulesSuite) TestWorkspaceModuleVersionUpdate(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	content := c.Directory().WithNewFile("dagger.json", `{"name":"tools","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("main.go", "package main\ntype Tools struct{}\n")
	repo := c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
		WithDirectory("/src", content).WithWorkdir("/src").WithExec([]string{"git", "init", "-b", "main"}).
		WithExec([]string{"git", "add", "."}).WithExec([]string{"git", "commit", "-m", "first"}).
		WithExec([]string{"git", "tag", "v1.0.0"}).
		WithNewFile("version.txt", "second").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "second"}).WithExec([]string{"git", "tag", "v2.0.0"})
	remote := newRemoteWorkspace(ctx, t, c, repo.Directory("/src"))
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"name flag", []string{"mod", "update", "tools", "--version=v2"}},
		{"name suffix", []string{"update", "tools@v2"}},
		{"source flag", []string{"mod", "update", remote.repoURL, "--version=v2"}},
		{"source suffix", []string{"mod", "update", remote.repoURL + "@v2"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			config := fmt.Sprintf("# Keep this comment\n[modules.tools]\nsource = '%s@v1' # request\nentrypoint = false\n", remote.repoURL)
			workdir := newWorkspaceConfigWorkdir(ctx, t, config)
			out, err := hostDaggerExecRaw(ctx, t, workdir, tc.args...)
			require.NoError(t, err)
			require.Contains(t, string(out), `Updating "tools": v1 -> v2.`)
			data, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
			require.NoError(t, err)
			require.Equal(t, strings.Replace(config, "@v1", "@v2", 1), string(data))
			lock, err := os.ReadFile(filepath.Join(workdir, workspace.LockFileName))
			require.NoError(t, err)
			require.Contains(t, string(lock), remote.commit)
			out, err = hostDaggerOutput(ctx, t, workdir, "mod", "version", "tools")
			require.NoError(t, err)
			require.Equal(t, "v2\n", string(out))
			_, err = hostDaggerExecRaw(ctx, t, workdir, "mod", "update", "tools")
			require.NoError(t, err)
			refreshed, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
			require.NoError(t, err)
			require.Equal(t, data, refreshed)
		})
	}
	t.Run("failed version leaves config and lock unchanged", func(ctx context.Context, t *testctx.T) {
		config := fmt.Sprintf("[modules.tools]\nsource = '%s@v1'\n", remote.repoURL)
		workdir := newWorkspaceConfigWorkdir(ctx, t, config)
		_, err := hostDaggerExecRaw(ctx, t, workdir, "mod", "update", "tools")
		require.NoError(t, err)
		lock, err := os.ReadFile(filepath.Join(workdir, workspace.LockFileName))
		require.NoError(t, err)
		_, err = hostDaggerExecRaw(ctx, t, workdir, "mod", "update", "tools@missing")
		require.Error(t, err)
		afterConfig, err := os.ReadFile(filepath.Join(workdir, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Equal(t, config, string(afterConfig))
		afterLock, err := os.ReadFile(filepath.Join(workdir, workspace.LockFileName))
		require.NoError(t, err)
		require.Equal(t, lock, afterLock)
	})
}
