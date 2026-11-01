package core

// These tests cover Query.serveModule called by a module's code: a local
// address names a module inside the tree the calling module was loaded from,
// never a path in the workspace of whoever called the module.
//
// See also:
// - module_serve_test.go: serveModule from a plain client.

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

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

// serveTreeCachedSource has the caller serve a sibling and call it, so the
// sibling runs as a module served from the caller's tree.
const serveTreeCachedSource = `package main

import "context"

func (m *Caller) Cached(ctx context.Context, address, token string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var log string
	q := dag.QueryBuilder().Select("hello").Select("cached").Arg("token", token).Bind(&log)
	return log, q.Execute(ctx)
}
`

// serveTreeChainSource has the caller serve the mid module, which serves the
// next sibling from its own tree in turn.
const serveTreeChainSource = `package main

import "context"

func (m *Caller) Chain(ctx context.Context, address, next, token string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var log string
	q := dag.QueryBuilder().Select("mid").Select("cached").Arg("address", next).Arg("token", token).Bind(&log)
	return log, q.Execute(ctx)
}
`

// serveTreeAppSource has the caller serve the app module, which reaches the
// sibling with the cache volume as its own declared dependency.
const serveTreeAppSource = `package main

import "context"

func (m *Caller) App(ctx context.Context, address, token string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var log string
	q := dag.QueryBuilder().Select("app").Select("cached").Arg("token", token).Bind(&log)
	return log, q.Execute(ctx)
}
`

const serveTreeAppManifest = `name = "app"
engineVersion = "latest"
source = "."

[runtime]
source = "dang"

[[dependencies]]
name = "hello"
source = "../hello"
`

const serveTreeAppDang = `type App {
  pub cached(token: String!): String! {
    hello.cached(token: token)
  }
}
`

// serveTreeCachedModules adds a sibling with a cache volume to the tree, a mid
// module, a copy of the caller, to serve it through, and an app module that
// depends on it.
func serveTreeCachedModules(c *dagger.Client) *core.Directory {
	return serveTreeModules(c).
		WithNewFile("modules/caller/cached.go", serveTreeCachedSource).
		WithNewFile("modules/caller/chain.go", serveTreeChainSource).
		WithNewFile("modules/caller/app.go", serveTreeAppSource).
		WithNewFile("modules/app/dagger-module.toml", serveTreeAppManifest).
		WithNewFile("modules/app/main.dang", serveTreeAppDang).
		WithNewFile("modules/hello/main.dang", serveTreeCachedHelloSource("serve-tree-"+rand.Text())).
		WithNewFile("modules/mid/dagger.json", strings.Replace(serveTreeCallerManifest, `"caller"`, `"mid"`, 1)).
		WithNewFile("modules/mid/main.go", strings.ReplaceAll(serveTreeCallerSource, "Caller", "Mid")).
		WithNewFile("modules/mid/cached.go", strings.ReplaceAll(serveTreeCachedSource, "Caller", "Mid"))
}

// serveTreeCachedHelloSource appends a token to a cache volume and returns
// every token appended so far, which shows the module the volume is shared
// with.
func serveTreeCachedHelloSource(key string) string {
	return fmt.Sprintf(`type Hello {
  pub message: String! {
    "hi from hello"
  }

  pub cached(token: String!): String! {
    container
      .from(%q)
      .withMountedCache("/cache", cacheVolume(%q))
      .withExec(["sh", "-c", "echo " + token + " >> /cache/log; cat /cache/log"])
      .stdout
  }
}
`, alpineImage, key)
}

// serveTreeModules is a tree holding a caller module and a sibling local
// client that nothing declares.
func serveTreeModules(c *dagger.Client) *core.Directory {
	return core.NewQuery(c).Directory().
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

	t.Run("sibling served from a git module shares the caches of the same module loaded directly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, serveTreeCachedModules(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n")

		out, err := ctr.
			With(daggerCallAt(repoURL+"#main:modules/caller", "cached", "--address=/modules/hello", "--token=served")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "served\n", out)

		out, err = ctr.
			With(daggerCallAt(repoURL+"#main:modules/hello", "cached", "--token=direct")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "served\ndirect\n", out)
	})

	t.Run("sibling served by a served sibling shares the caches of the same module loaded directly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, serveTreeCachedModules(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n")

		out, err := ctr.
			With(daggerCallAt(repoURL+"#main:modules/caller", "chain", "--address=/modules/mid", "--next=../hello", "--token=chained")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "chained\n", out)

		out, err = ctr.
			With(daggerCallAt(repoURL+"#main:modules/hello", "cached", "--token=direct")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "chained\ndirect\n", out)
	})

	t.Run("dependency of a served sibling shares the caches of the same module loaded directly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, serveTreeCachedModules(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n")

		out, err := ctr.
			With(daggerCallAt(repoURL+"#main:modules/caller", "app", "--address=/modules/app", "--token=dependency")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "dependency\n", out)

		out, err = ctr.
			With(daggerCallAt(repoURL+"#main:modules/hello", "cached", "--token=direct")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "dependency\ndirect\n", out)
	})

	t.Run("sibling served from a host module shares the caches of the same module loaded directly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeCachedModules(c))

		out, err := ctr.
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token=served")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "served\n", out)

		out, err = ctr.
			With(daggerCallAt("./modules/hello", "cached", "--token=direct")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "served\ndirect\n", out)
	})

	t.Run("directory module serves a sibling from its own directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		require.NoError(t, serveTreeModules(c).
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "modules/caller"}).
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
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "modules/caller"}).
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
