package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const sdkCLIConfig = `[modules.go-sdk]
source = "github.com/dagger/go-sdk"

[modules.python-sdk]
source = "github.com/dagger/python-sdk"

[sdks.go]
module = "go-sdk"

[sdks.go.scopes."apps/api"]
is-module = true
name = "api"
clients = ["database"]

[sdks.go.scopes."apps/api".settings]
mode = "fast"

[sdks.python]
module = "python-sdk"

[sdks.python.scopes."apps/web"]
name = "web"
clients = ["frontend"]
`

func newSDKCLIWorkdir(ctx context.Context, t *testctx.T) string {
	t.Helper()
	workdir := newWorkspaceConfigWorkdir(ctx, t, sdkCLIConfig)
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "apps", "api", "internal"), 0o755))
	return workdir
}

func (WorkspaceSuite) TestSDKListCLI(ctx context.Context, t *testctx.T) {
	workdir := newSDKCLIWorkdir(ctx, t)

	out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "list")
	require.NoError(t, err)
	require.Equal(t, []string{
		"SDK", "SOURCE",
		"go", "github.com/dagger/go-sdk",
		"python", "github.com/dagger/python-sdk",
	}, strings.Fields(string(out)))
}

func (WorkspaceSuite) TestSDKScopeListCLI(ctx context.Context, t *testctx.T) {
	workdir := newSDKCLIWorkdir(ctx, t)

	t.Run("all", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "list")
		require.NoError(t, err)
		require.Equal(t, []string{
			"NAME", "PATH", "SDK", "IS-MODULE",
			"api", "apps/api", "go", "true",
			"web", "apps/web", "python", "false",
		}, strings.Fields(string(out)))
	})

	t.Run("is-module", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "list", "--is-module=false")
		require.NoError(t, err)
		require.Equal(t, []string{
			"NAME", "PATH", "SDK", "IS-MODULE",
			"web", "apps/web", "python", "false",
		}, strings.Fields(string(out)))
	})

	t.Run("name and sdk", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "list", "--name=api", "--sdk=go")
		require.NoError(t, err)
		require.Equal(t, []string{
			"NAME", "PATH", "SDK", "IS-MODULE",
			"api", "apps/api", "go", "true",
		}, strings.Fields(string(out)))
	})

	t.Run("module clients use the same scope paths", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "module", "client", "list", "--all")
		require.NoError(t, err)
		require.Equal(t, []string{
			"SCOPE", "SDK", "TARGET",
			"apps/api", "go", "database",
			"apps/web", "python", "frontend",
		}, strings.Fields(string(out)))
	})
}

func (WorkspaceSuite) TestSDKScopeFieldsCLI(ctx context.Context, t *testctx.T) {
	t.Run("get from cwd or path", func(ctx context.Context, t *testctx.T) {
		workdir := newSDKCLIWorkdir(ctx, t)
		cwd := filepath.Join(workdir, "apps", "api", "internal")

		out, err := hostDaggerOutput(ctx, t, cwd, "--silent", "sdk", "scope", "name")
		require.NoError(t, err)
		require.Equal(t, "api", strings.TrimSpace(string(out)))

		out, err = hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "is-module")
		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(string(out)))

		out, err = hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "sdk", "--path=apps/api")
		require.NoError(t, err)
		require.Equal(t, "go", strings.TrimSpace(string(out)))
	})

	t.Run("set and unset fields", func(ctx context.Context, t *testctx.T) {
		workdir := newSDKCLIWorkdir(ctx, t)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "name", "service")
		require.NoError(t, err)
		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "service", cfg.SDKs["go"].Scopes["apps/api"].Name)

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "name", "-u")
		require.Error(t, err)
		requireErrOut(t, err, "scope name is required when is-module is true")

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "is-module", "false")
		require.NoError(t, err)
		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.False(t, cfg.SDKs["go"].Scopes["apps/api"].IsModule)

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "name", "-u")
		require.NoError(t, err)
		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.Empty(t, cfg.SDKs["go"].Scopes["apps/api"].Name)

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "name", "service")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "is-module", "true")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "is-module", "-u")
		require.NoError(t, err)
		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.False(t, cfg.SDKs["go"].Scopes["apps/api"].IsModule)
	})

	t.Run("move and remove scope", func(ctx context.Context, t *testctx.T) {
		workdir := newSDKCLIWorkdir(ctx, t)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "sdk", "python")
		require.NoError(t, err)
		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.SDKs["go"].Scopes, "apps/api")
		moved := cfg.SDKs["python"].Scopes["apps/api"]
		require.Equal(t, "api", moved.Name)
		require.True(t, moved.IsModule)
		require.Equal(t, []string{"database"}, moved.Clients)
		require.Equal(t, "fast", moved.Settings["mode"])

		out, err := hostDaggerOutput(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "sdk")
		require.NoError(t, err)
		require.Equal(t, "python", strings.TrimSpace(string(out)))

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "sdk", "scope", "--path=apps/api", "sdk", "-u")
		require.NoError(t, err)
		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.SDKs["python"].Scopes, "apps/api")
	})
}
