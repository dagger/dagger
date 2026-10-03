package core

// These tests cover Query.serveModule on a private git address from a
// module's code: the module's own config must declare the client for the
// session's git credentials to be used, as a module's config declares a
// dependency.

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const serveGitTrustToken = "serve-git-trust-token"

// serveGitTrustCallerSource adds to the tree tests' caller a function that
// returns the error serving an address gives the module's code.
const serveGitTrustCallerSource = serveTreeCallerSource + `
func (m *Caller) Attempt(ctx context.Context, address string) string {
	if err := dag.ServeModule(ctx, address); err != nil {
		return err.Error()
	}
	return "served"
}
`

// serveGitTrustPrivateRepo serves a repository that requires credentials,
// holding a hello module and another module, and returns its URL by IP so
// nested sessions reach it as the engine does.
func serveGitTrustPrivateRepo(ctx context.Context, t *testctx.T, c *dagger.Client) string {
	t.Helper()

	content := c.Directory().
		WithNewFile("hello/dagger-module.toml", serveModuleHelloManifest).
		WithNewFile("hello/main.dang", serveModuleHelloSource).
		WithNewFile("other/dagger-module.toml", strings.ReplaceAll(serveModuleHelloManifest, `"hello"`, `"other"`)).
		WithNewFile("other/main.dang", strings.ReplaceAll(serveModuleHelloSource, "Hello", "Other"))
	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, content, "main"), "", c.SetSecret("serve-git-trust", serveGitTrustToken))
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	host, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)
	out, err := c.Container().From(alpineImage).WithExec([]string{"getent", "hosts", host}).Stdout(ctx)
	require.NoError(t, err)
	fields := strings.Fields(out)
	require.NotEmpty(t, fields, "unexpected getent output: %q", out)
	return "http://" + fields[0] + "/repo.git"
}

// serveGitTrustBase is a CLI container whose git credential helper answers
// for the private repository's host.
func serveGitTrustBase(t *testctx.T, c *dagger.Client, repoURL string) *dagger.Container {
	t.Helper()

	host := strings.TrimSuffix(repoURL, "/repo.git")
	helper := fmt.Sprintf(`!f() { test "$1" = get && printf 'username=x-access-token\npassword=%s\n'; }; f`, serveGitTrustToken)
	return goGitBase(t, c).
		WithExec([]string{"git", "config", "--global", "credential." + host + ".helper", helper})
}

// serveGitTrustWorkspaceConfig declares the caller module, and client as its
// one git client unless it is empty.
func serveGitTrustWorkspaceConfig(client string) string {
	cfg := `[modules.caller]
source = "modules/caller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes."modules/caller"]
is-module = true
`
	if client != "" {
		cfg += fmt.Sprintf("clients = [%q]\n", client)
	}
	return cfg
}

func (ModuleLoadingSuite) TestServeModuleDeclaredGitClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	repoURL := serveGitTrustPrivateRepo(ctx, t, c)
	hello := repoURL + "/hello"

	hostCaller := func(client string) *dagger.Container {
		return serveGitTrustBase(t, c, repoURL).
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(client)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveGitTrustCallerSource)
	}

	t.Run("module serves a client its config declares", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller(hello).
			With(daggerCallAt("caller", "message", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})

	t.Run("module serving an undeclared client gets a hint", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller(hello).
			With(daggerCallAt("caller", "attempt", "--address="+repoURL+"/other")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "git authentication failed")
		require.Contains(t, out, `declare it as a client of module "caller"'s scope`)
	})

	t.Run("nothing declared fails closed", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller("").
			With(daggerCallAt("caller", "attempt", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "git authentication failed")
		require.Contains(t, out, `declare it as a client of module "caller"'s scope`)
	})

	t.Run("a plain program keeps its own credentials", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller("").
			With(daggerQuery(`{serveModule(address: %q)}`, hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"serveModule": null}`, out)
	})

	t.Run("module loaded from git serves a client its own config declares", func(ctx context.Context, t *testctx.T) {
		// The caller's repository holds the declaration, not the workspace
		// calling it, which declares nothing.
		callerRepo := c.Directory().
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(hello)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveTreeCallerSource)
		gitDaemon, callerURL := gitService(ctx, t, c, callerRepo)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		out, err := serveGitTrustBase(t, c, repoURL).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n").
			With(daggerCallAt(callerURL+"#main:modules/caller", "message", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})
}
