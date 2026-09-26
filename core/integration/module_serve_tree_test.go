package core

// These tests cover Query.serveModule called by a module's code: a local
// address names a module inside the tree the calling module was loaded from,
// never a path in the workspace of whoever called the module.
//
// See also:
// - module_serve_test.go: serveModule from a plain client.

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const serveTreeCallerManifest = `{"name":"caller","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`

const serveTreeCallerSource = `package main

import "context"

type Caller struct{}

func (m *Caller) Message(ctx context.Context, address string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var message string
	q := dag.QueryBuilder().Select("hello").Select("message").Bind(&message)
	return message, q.Execute(ctx)
}
`

const serveTreeDecoySource = `type Hello {
  pub message: String! {
    "hi from the caller's workspace"
  }
}
`

// serveTreeNestedSource has the caller serve from a plain process it starts,
// which the engine does not make the module.
var serveTreeNestedSource = fmt.Sprintf(`package main

import (
	"context"

	"dagger/caller/internal/dagger"
)

const nestedServe = "apk add -q curl && " +
	"q() { curl -s -u \"$DAGGER_SESSION_TOKEN:\" -H 'Content-Type: application/json' -d \"$1\" \"http://127.0.0.1:$DAGGER_SESSION_PORT/query\"; echo; } && " +
	"q '{\"query\":\"{serveModule(address: \\\"/modules/hello\\\")}\"}' && " +
	"q '{\"query\":\"{hello{message}}\"}'"

func (m *Caller) NestedMessage(ctx context.Context) (string, error) {
	return dag.Container().From(%q).
		WithExec([]string{"sh", "-c", nestedServe}, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}).
		Stdout(ctx)
}
`, alpineImage)

// serveTreeModules is a tree holding a caller module and a sibling local
// client that nothing declares.
func serveTreeModules(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
		WithNewFile("modules/caller/main.go", serveTreeCallerSource).
		WithNewFile("modules/hello/dagger-module.toml", serveModuleHelloManifest).
		WithNewFile("modules/hello/main.dang", serveModuleHelloSource)
}

func (ModuleLoadingSuite) TestServeModuleCallerTree(ctx context.Context, t *testctx.T) {
	t.Run("git module serves a sibling from its own commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, serveTreeModules(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		out, err := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n").
			WithNewFile("modules/hello/dagger-module.toml", serveModuleHelloManifest).
			WithNewFile("modules/hello/main.dang", serveTreeDecoySource).
			With(daggerCallAt(repoURL+"#main:modules/caller", "message", "--address=/modules/hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})

	t.Run("directory module serves a sibling from its own directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		require.NoError(t, serveTreeModules(c).
			AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: "modules/caller"}).
			AsModule().
			Serve(ctx))

		res, err := testutil.QueryWithClient[struct {
			Caller struct {
				Message string
			}
		}](c, t, `{caller{message(address: "../hello")}}`, nil)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", res.Caller.Message)
	})

	t.Run("path leaving the tree is refused", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		require.NoError(t, serveTreeModules(c).
			AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: "modules/caller"}).
			AsModule().
			Serve(ctx))

		_, err := testutil.QueryWithClient[struct {
			Caller struct {
				Message string
			}
		}](c, t, `{caller{message(address: "../../../outside")}}`, nil)
		require.ErrorContains(t, err, "leaves the module's own tree")
	})

	t.Run("symlink leaving a host module's tree is refused", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeModules(c)).
			WithNewFile("/outside/hello/dagger-module.toml", serveModuleHelloManifest).
			WithNewFile("/outside/hello/main.dang", serveTreeDecoySource).
			WithExec([]string{"ln", "-s", "/outside/hello", "modules/escape"}).
			With(daggerCallAt("caller", "message", "--address=/modules/escape")).
			Stdout(ctx)
		requireErrOut(t, err, "leaves the module's own tree through a symlink")
	})

	t.Run("process a host module starts serves from the module's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeModules(c)).
			WithNewFile("modules/caller/nested.go", serveTreeNestedSource).
			With(daggerCallAt("caller", "nested-message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"message":"hi from hello"`)
	})

	t.Run("process without a module resolves in its workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", "\n").
			WithNewFile("modules/hello/dagger-module.toml", serveModuleHelloManifest).
			WithNewFile("modules/hello/main.dang", serveModuleHelloSource).
			With(daggerQuery(`{serveModule(address: "/modules/hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"serveModule": null}`, out)
	})
}
