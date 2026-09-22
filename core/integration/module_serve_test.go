package core

// These tests cover Query.serveModule: resolving a module address — remote, or
// a path inside the caller's workspace — and installing it into the calling
// session's schema.
//
// See also:
// - workspace_api_test.go: Workspace.moduleSource, the local resolution this
//   builds on.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const serveModuleHelloManifest = `name = "hello"
engineVersion = "latest"
source = "."

[runtime]
source = "dang"
`

const serveModuleHelloSource = `type Hello {
  pub message: String! {
    "hi from hello"
  }
}
`

// serveModuleWorkdir lays out a workspace whose hello module is deliberately
// *not* installed in dagger.toml: nothing serves it ambiently, so reaching it
// proves serveModule installed it.
func serveModuleWorkdir(ctx context.Context, t *testctx.T) string {
	t.Helper()

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	for path, contents := range map[string]string{
		"dagger.toml": "\n",
		".dagger/modules/hello/dagger-module.toml": serveModuleHelloManifest,
		".dagger/modules/hello/main.dang":          serveModuleHelloSource,
		"nested/marker":                            "x",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(workdir, path)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workdir, path), []byte(contents), 0o600))
	}
	return workdir
}

func requireServedHello(t *testctx.T, c *dagger.Client) {
	t.Helper()

	res, err := testutil.QueryWithClient[struct {
		Hello struct {
			Message string
		}
	}](c, t, `{hello{message}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "hi from hello", res.Hello.Message)
}

func (ModuleLoadingSuite) TestServeModuleLocalAddress(ctx context.Context, t *testctx.T) {
	t.Run("absolute path resolves from the workspace root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t, dagger.WithWorkdir(serveModuleWorkdir(ctx, t)))
		require.NoError(t, c.ServeModule(ctx, "/.dagger/modules/hello"))
		requireServedHello(t, c)
	})

	t.Run("relative path resolves from the workspace cwd", func(ctx context.Context, t *testctx.T) {
		workdir := serveModuleWorkdir(ctx, t)
		c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(workdir, "nested")))
		require.NoError(t, c.ServeModule(ctx, "../.dagger/modules/hello"))
		requireServedHello(t, c)
	})

	t.Run("path without a module errors", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t, dagger.WithWorkdir(serveModuleWorkdir(ctx, t)))
		err := c.ServeModule(ctx, "/nested")
		require.ErrorContains(t, err, "does not contain a dagger config file")
	})

	t.Run("installed module name is rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t, dagger.WithWorkdir(serveModuleWorkdir(ctx, t)))
		err := c.ServeModule(ctx, "hello")
		require.ErrorContains(t, err, "installed module names are not accepted")
	})
}

// A generated client baked into a module is the reason serveModule exists: the
// workspace APIs are absent from a module's schema, so the module cannot
// resolve a local address itself.
func (ModuleLoadingSuite) TestServeModuleFromModule(ctx context.Context, t *testctx.T) {
	callerModule := func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithNewFile("dagger.toml", `[modules.caller]
source = ".dagger/modules/caller"
`).
			// Legacy manifest so the Go runtime generates its bindings at load
			// time: a dagger-module.toml module builds from committed generated
			// files, which a fixture written inline has no way to carry.
			WithNewFile(".dagger/modules/caller/dagger.json", `{"name":"caller","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
			WithNewFile(".dagger/modules/caller/main.go", `package main

import "context"

type Caller struct{}

// The served module has no generated bindings here, so it is reached through a
// raw selection -- what a generated client's own bootstrap amounts to.
func (m *Caller) Message(ctx context.Context, address string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var message string
	q := dag.QueryBuilder().Select("hello").Select("message").Bind(&message)
	return message, q.Execute(ctx)
}
`)
	}

	t.Run("local address resolves against the calling module's workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			With(callerModule).
			WithNewFile(".dagger/modules/hello/dagger-module.toml", serveModuleHelloManifest).
			WithNewFile(".dagger/modules/hello/main.dang", serveModuleHelloSource).
			With(daggerCallAt("caller", "message", "--address=/.dagger/modules/hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})

	t.Run("remote address resolves without a workspace lookup", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		served := c.Directory().
			WithNewFile("dagger-module.toml", serveModuleHelloManifest).
			WithNewFile("main.dang", serveModuleHelloSource)
		gitDaemon, repoURL := gitService(ctx, t, c, served)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		out, err := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			With(callerModule).
			// The explicit protocol://repo#ref form: a bare scheme URL would be
			// resolved as a Go import path, which this daemon's hostname is not.
			With(daggerCallAt("caller", "message", "--address="+repoURL+"#main")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})
}

// A generated client records a readable version query and the commit its lock
// resolved. serveModule must keep loading that commit after a newer tag starts
// matching the query, while moduleSource still requires the two to agree.
func (ModuleLoadingSuite) TestServeModulePinnedVersionQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	repo := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/src").
		WithExec([]string{"git", "init", "-b", "main"}).
		WithNewFile("dagger-module.toml", serveModuleHelloManifest).
		WithNewFile("main.dang", strings.ReplaceAll(serveModuleHelloSource, "hi from hello", "hi from v1.0.0")).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "v1.0.0"}).
		WithExec([]string{"git", "tag", "v1.0.0"}).
		WithNewFile("main.dang", strings.ReplaceAll(serveModuleHelloSource, "hi from hello", "hi from v1.0.1")).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "v1.0.1"}).
		WithExec([]string{"git", "tag", "v1.0.1"})
	remote := newRemoteWorkspace(ctx, t, c, repo.Directory("/src"))
	pinned, err := c.Git(remote.repoURL).Tag("v1.0.0").CommitSHA(ctx)
	require.NoError(t, err)
	latest, err := c.Git(remote.repoURL).Tag("v1.0.1").CommitSHA(ctx)
	require.NoError(t, err)
	address := remote.repoURL + "@v1"

	t.Run("serveModule loads the pinned commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t, dagger.WithWorkdir(t.TempDir()))
		require.NoError(t, c.ServeModule(ctx, address, dagger.ServeModuleOpts{RefPin: pinned}))

		res, err := testutil.QueryWithClient[struct {
			Hello struct {
				Message string
			}
		}](c, t, `{hello{message}}`, nil)
		require.NoError(t, err)
		require.Equal(t, "hi from v1.0.0", res.Hello.Message)
	})

	t.Run("moduleSource still requires the query and the pin to agree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t, dagger.WithWorkdir(t.TempDir()))
		_, err := c.ModuleSource(address, dagger.ModuleSourceOpts{RefPin: pinned}).Digest(ctx)
		require.ErrorContains(t, err, `version query "v1" resolved to Git ref`)
		require.ErrorContains(t, err, fmt.Sprintf("at commit %q, but the requested pin is %q", latest, pinned))
	})
}
