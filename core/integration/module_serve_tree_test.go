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
	"regexp"
	"slices"
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

// serveTreeWorkspaceNestedScript is what a process a module starts runs: it
// loads the caller through the workspace the module was given, as a test
// binary's client does, then has the caller serve its sibling.
const serveTreeWorkspaceNestedScript = `apk add -q curl
q() { curl -s -u "$DAGGER_SESSION_TOKEN:" -H 'Content-Type: application/json' -d "$1" "http://127.0.0.1:$DAGGER_SESSION_PORT/query"; echo; }
q "{\"query\":\"{node(id: \\\"$WS_ID\\\"){... on Workspace{moduleSource(path: \\\"/modules/caller\\\"){asModule{serve}}}}}\"}"
q '{"query":"{caller{message(address: \"/modules/hello\")}}"}'
`

// serveTreeWorkspaceNestedSource is a runner module whose process loads the
// caller from the runner's workspace.
var serveTreeWorkspaceNestedSource = fmt.Sprintf(`package main

import (
	"context"

	"dagger/runner/internal/dagger"
)

type Runner struct{}

func (m *Runner) NestedWorkspaceMessage(ctx context.Context, ws *dagger.Workspace) (string, error) {
	id, err := ws.ID(ctx)
	if err != nil {
		return "", err
	}
	return dag.Container().From(%q).
		WithEnvVariable("WS_ID", string(id)).
		WithExec([]string{"sh", "-c", %q}, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}).
		Stdout(ctx)
}
`, alpineImage, serveTreeWorkspaceNestedScript)

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

// serveTreeGitCallerSource has the caller serve a sibling and read the
// repository the sibling's contextual GitRepository argument resolves to,
// through the head function of the sibling's top-level field.
const serveTreeGitCallerSource = `package main

import "context"

func (m *Caller) GitHead(
	ctx context.Context,
	address string,
	// +default="gitctx"
	field string,
) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var head string
	q := dag.QueryBuilder().Select(field).Select("head").Bind(&head)
	return head, q.Execute(ctx)
}
`

// serveTreeGitctxSource reports the commit its contextual repository's HEAD
// points at, or that it got no repository.
const serveTreeGitctxSource = `package main

import (
	"context"

	"dagger/gitctx/internal/dagger"
)

type Gitctx struct{}

func (m *Gitctx) Head(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.GitRepository,
) (string, error) {
	if repo == nil {
		return "no repository", nil
	}
	return repo.Head().Commit(ctx)
}
`

// serveTreeGitModules adds a sibling with a contextual GitRepository argument
// to the tree, and a caller function that serves it.
func serveTreeGitModules(c *dagger.Client) *core.Directory {
	return serveTreeModules(c).
		WithNewFile("modules/caller/githead.go", serveTreeGitCallerSource).
		WithNewFile("modules/gitctx/dagger.json", strings.Replace(serveTreeCallerManifest, `"caller"`, `"gitctx"`, 1)).
		WithNewFile("modules/gitctx/main.go", serveTreeGitctxSource).
		WithNewFile("modules/caller/chain.go", serveTreeGitChainSource).
		WithNewFile("modules/mid/dagger.json", strings.Replace(serveTreeCallerManifest, `"caller"`, `"mid"`, 1)).
		WithNewFile("modules/mid/main.go", strings.ReplaceAll(serveTreeCallerSource, "Caller", "Mid")).
		WithNewFile("modules/mid/githead.go", strings.ReplaceAll(serveTreeGitCallerSource, "Caller", "Mid")).
		WithNewFile("modules/usesgit/dagger.json", serveTreeUsesgitManifest).
		WithNewFile("modules/usesgit/main.go", serveTreeUsesgitSource)
}

// serveTreeUsesgitManifest is a module depending on the git sibling.
const serveTreeUsesgitManifest = `{"name":"usesgit","engineVersion":"latest","sdk":{"source":"go"},"source":".","dependencies":[{"name":"gitctx","source":"../gitctx"}]}`

// serveTreeUsesgitSource exposes its dependency's contextual repository as
// head, so a caller serving it reads what the dependency resolved.
const serveTreeUsesgitSource = `package main

import "context"

type Usesgit struct{}

func (m *Usesgit) Head(ctx context.Context) (string, error) {
	return dag.Gitctx().Head(ctx)
}
`

// serveTreeGitChainSource has the caller serve the mid module, which serves
// the git sibling from its own tree in turn.
const serveTreeGitChainSource = `package main

import "context"

func (m *Caller) GitChain(ctx context.Context, address, next string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var head string
	q := dag.QueryBuilder().Select("mid").Select("gitHead").Arg("address", next).Bind(&head)
	return head, q.Execute(ctx)
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

// serveTreeOtherHelloSource is a different sibling with the same API.
const serveTreeOtherHelloSource = `type Hello {
  pub message: String! {
    "hi from the other hello"
  }

  pub cached(token: String!): String! {
    "other " + token
  }
}
`

// serveTreeEntrypointCaller is a caller module with an entrypoint: like an
// SDK's generated one, it runs each function in a process it starts, and
// that process serves the sibling and calls it.
var serveTreeEntrypointCaller = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Caller")
        .withConstructor(function("", typeDef.withObject("Caller")))
        .withFunction(function("Cached", typeDef.withKind(TypeDefKind.STRING_KIND))
          .withArg("token", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Shared", typeDef.withKind(TypeDefKind.STRING_KIND))
          .withArg("token", typeDef.withKind(TypeDefKind.STRING_KIND))
          .withCachePolicy(FunctionCachePolicy.Never)),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      (JSON.encode(container
        .from(%q)
        .withNewFile("/args.json", JSON.encode(fnArgs))
        .withNewFile("/serve.sh", %q)
        .withExec(["sh", "/serve.sh"], experimentalPrivilegedNesting: true)
        .stdout) :: JSON!)
    }
  }
}
`, alpineImage, serveTreeEntrypointScript)

// serveTreeEntrypointScript serves the sibling from the process and asks it
// for the call's token.
const serveTreeEntrypointScript = `set -e
apk add -q curl
TOKEN=$(sed 's/.*token[^a-z]*\([a-z]*\).*/\1/' /args.json)
q() { curl -s -u "$DAGGER_SESSION_TOKEN:" -H 'Content-Type: application/json' -d "$1" "http://127.0.0.1:$DAGGER_SESSION_PORT/query"; echo; }
q '{"query":"{serveModule(address: \"/modules/hello\")}"}'
q "{\"query\":\"{hello{cached(token: \\\"$TOKEN\\\")}}\"}"
echo "process-run-$(cat /proc/sys/kernel/random/uuid)"
`

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

// serveTreeProcessRun finds the line serveTreeEntrypointScript prints with a
// value new to each run of the process.
var serveTreeProcessRun = regexp.MustCompile(`process-run-[0-9a-f-]+`)

// serveTreeToken is a token of lowercase letters, which is all
// serveTreeEntrypointScript reads.
func serveTreeToken() string {
	return strings.Map(func(r rune) rune {
		if r >= '2' && r <= '7' {
			return -1
		}
		return r
	}, strings.ToLower(rand.Text()))
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

	t.Run("sibling served from a host module resolves its contextual git to the host checkout", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeGitModules(c)).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"})
		commit, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
		require.NoError(t, err)
		commit = strings.TrimSpace(commit)

		out, err := ctr.
			With(daggerCallAt("./modules/gitctx", "head")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "loaded directly")

		out, err = ctr.
			With(daggerCallAt("caller", "git-head", "--address=/modules/gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "served from the caller's tree")

		out, err = ctr.
			With(daggerCallAt("caller", "git-chain", "--address=/modules/mid", "--next=../gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "served by a module served from the caller's tree")

		out, err = ctr.
			With(daggerCallAt("caller", "git-head", "--address=/modules/usesgit", "--field=usesgit")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "dependency of a module served from the caller's tree")
	})

	t.Run("sibling served from a git workspace's module resolves its contextual git to the workspace's commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// Branch next is an empty commit on top of main: the same tree at
		// another commit.
		gitDir := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithDirectory("/srv", makeGitDir(c, serveTreeGitModules(c).
				WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"

[modules.gitctx]
source = "modules/gitctx"
`), "main")).
			WithExec([]string{"sh", "-c", "git config --global user.email root@localhost && git config --global user.name Test && " +
				"git clone --no-local /srv/repo.git /work && cd /work && git checkout -b next && git commit --allow-empty -m next && git push origin next && " +
				"cd /srv/repo.git && git update-server-info"}).
			Directory("/srv")
		repo := workspaceSelectionRemoteRepo(ctx, t, c, gitDir)
		ref := repo + "@main"
		ctr := nativeWorkspaceBase(t, c)

		commit, err := ctr.
			With(workspaceSelectionDaggerExec("-W", ref, "call", "gitctx", "head")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `^[0-9a-f]{40}$`, commit, "loaded directly")

		out, err := ctr.
			With(workspaceSelectionDaggerExec("-W", ref, "call", "caller", "git-head", "--address=/modules/gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "served from the caller's tree")

		out, err = ctr.
			With(workspaceSelectionDaggerExec("-W", ref, "call", "caller", "git-chain", "--address=/modules/mid", "--next=../gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "served by a module served from the caller's tree")

		out, err = ctr.
			With(workspaceSelectionDaggerExec("-W", ref, "call", "caller", "git-head", "--address=/modules/usesgit", "--field=usesgit")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "dependency of a module served from the caller's tree")

		// The same tree at another commit resolves to that commit, through
		// served modules whose own files are unchanged.
		next := repo + "@next"
		nextCommit, err := ctr.
			With(workspaceSelectionDaggerExec("-W", next, "call", "gitctx", "head")).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEqual(t, commit, nextCommit)

		out, err = ctr.
			With(workspaceSelectionDaggerExec("-W", next, "call", "caller", "git-chain", "--address=/modules/mid", "--next=../gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, nextCommit, out, "served by a module served from the caller's tree, at another commit")

		out, err = ctr.
			With(workspaceSelectionDaggerExec("-W", next, "call", "caller", "git-head", "--address=/modules/usesgit", "--field=usesgit")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, nextCommit, out, "dependency of a module served from the caller's tree, at another commit")
	})

	t.Run("sibling served from a git module resolves its contextual git to the module's commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, serveTreeGitModules(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n")

		commit, err := ctr.
			With(daggerCallAt(repoURL+"#main:modules/gitctx", "head")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `^[0-9a-f]{40}$`, commit, "loaded directly")

		out, err := ctr.
			With(daggerCallAt(repoURL+"#main:modules/caller", "git-head", "--address=/modules/gitctx")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, commit, out, "served from the caller's tree")
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

	t.Run("module a nested client loads from its workspace serves from the workspace's host", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// The runner's process is a client nested in a container that does
		// not hold the workspace, so the caller's tree has to be read from the
		// host the workspace belongs to.
		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.runner]
source = "modules/runner"
`).
			WithDirectory(".", serveTreeModules(c)).
			WithNewFile("modules/runner/dagger.json", strings.Replace(serveTreeCallerManifest, `"caller"`, `"runner"`, 1)).
			WithNewFile("modules/runner/main.go", serveTreeWorkspaceNestedSource).
			With(daggerCallAt("runner", "nested-workspace-message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"message":"hi from hello"`)
	})

	// A module result is shared by sources with the same content, wherever
	// they were loaded from. Serving from the module's tree must still read
	// the tree the current session loaded it from. Each call passes its own
	// token, so it runs rather than hitting the previous call's cache.
	t.Run("module cached from another host serves from this host's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		tree := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeCachedModules(c))

		first, second := serveTreeToken(), serveTreeToken()
		out, err := tree.
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+first)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, first+"\n", out)

		// The same tree at another path on another host.
		out, err = goGitBase(t, c).
			WithDirectory("/elsewhere", tree.Directory("/work")).
			WithWorkdir("/elsewhere").
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, first+"\n"+second+"\n", out)
	})

	t.Run("module cached from another checkout serves from this checkout's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		manifest := `[modules.caller]
source = "modules/caller"
`
		// Two checkouts on one host: the same caller, different siblings.
		ctr := goGitBase(t, c).
			WithNewFile("/a/dagger.toml", manifest).
			WithDirectory("/a", serveTreeCachedModules(c)).
			WithNewFile("/b/dagger.toml", manifest).
			WithDirectory("/b", serveTreeCachedModules(c).WithNewFile("modules/hello/main.dang", serveTreeOtherHelloSource)).
			WithWorkdir("/a").
			WithExec([]string{"git", "init", "-q"}).
			WithWorkdir("/b").
			WithExec([]string{"git", "init", "-q"})

		first, second := serveTreeToken(), serveTreeToken()
		ctr = ctr.WithWorkdir("/a").
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+first))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, first+"\n", out)

		out, err = ctr.WithWorkdir("/b").
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other "+second, out)
	})

	t.Run("module served from its tree in one session serves again in the next", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		first, second := serveTreeToken(), serveTreeToken()
		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.caller]
source = "modules/caller"
`).
			WithDirectory(".", serveTreeCachedModules(c)).
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+first)).
			With(daggerCallAt("caller", "cached", "--address=/modules/hello", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, first+"\n"+second+"\n", out)
	})

	t.Run("entrypoint module cached from another host serves from this host's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		tree := goGitBase(t, c).
			WithDirectory(".", serveTreeCachedModules(c)).
			WithNewFile("modules/caller/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "caller"`, 1)).
			WithNewFile("modules/caller/entrypoint/main.dang", serveTreeEntrypointCaller).
			WithoutFile("modules/caller/dagger.json")

		first, second := serveTreeToken(), serveTreeToken()
		out, err := tree.
			With(daggerCallAt("./modules/caller", "cached", "--token="+first)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"cached":"`+first+`\n"`)

		// The same tree at another path, on a host without the first path.
		out, err = tree.
			WithDirectory("/elsewhere", tree.Directory("/work")).
			WithWorkdir("/elsewhere").
			WithoutDirectory("/work").
			With(daggerCallAt("./modules/caller", "cached", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, second+`\n"`)
	})

	// Serving from the client's own tree must not enter the process's
	// identity: the same module at another path still shares the process.
	// Shared is never cached, so each call runs the entrypoint; a process
	// that ran again would print a new run marker.
	t.Run("entrypoint module at another path shares its process", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		token := serveTreeToken()
		tree := goGitBase(t, c).
			WithDirectory(".", serveTreeCachedModules(c)).
			WithNewFile("modules/caller/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "caller"`, 1)).
			WithNewFile("modules/caller/entrypoint/main.dang", serveTreeEntrypointCaller).
			WithoutFile("modules/caller/dagger.json")

		out, err := tree.
			With(daggerCallAt("./modules/caller", "shared", "--token="+token)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"cached":"`+token+`\n"`)
		firstRun := serveTreeProcessRun.FindString(out)
		require.NotEmpty(t, firstRun)

		out, err = tree.
			WithDirectory("/elsewhere", tree.Directory("/work")).
			WithWorkdir("/elsewhere").
			WithoutDirectory("/work").
			With(daggerCallAt("./modules/caller", "shared", "--token="+token)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, firstRun, serveTreeProcessRun.FindString(out), "the process ran again: %s", out)
	})

	// The same entrypoint module in two checkouts on one host, with different
	// siblings and different arguments.
	t.Run("entrypoint module cached from another checkout serves from this checkout's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		first, second := serveTreeToken(), serveTreeToken()
		entrypointFiles := func(d *core.Directory) *core.Directory {
			return d.
				WithNewFile("modules/caller/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "caller"`, 1)).
				WithNewFile("modules/caller/entrypoint/main.dang", serveTreeEntrypointCaller).
				WithoutFile("modules/caller/dagger.json")
		}
		ctr := goGitBase(t, c).
			WithDirectory("/a", entrypointFiles(serveTreeCachedModules(c))).
			WithDirectory("/b", entrypointFiles(serveTreeCachedModules(c).WithNewFile("modules/hello/main.dang", serveTreeOtherHelloSource))).
			WithWorkdir("/a").
			WithExec([]string{"git", "init", "-q"}).
			WithWorkdir("/b").
			WithExec([]string{"git", "init", "-q"})

		ctr = ctr.WithWorkdir("/a").
			With(daggerCallAt("./modules/caller", "cached", "--token="+first))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"cached":"`+first+`\n"`)

		out, err = ctr.WithWorkdir("/b").
			With(daggerCallAt("./modules/caller", "cached", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"cached":"other `+second+`"`)
	})

	// The entrypoint case on a second host, with each CLI in a session of its
	// own.
	t.Run("entrypoint module cached from another session serves from this host's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		first, second := serveTreeToken(), serveTreeToken()
		newSessionCall := func(token string) core.WithContainerFunc {
			return func(ctr *core.Container) *core.Container {
				return ctr.WithExec([]string{"dagger", "call", "-m", "./modules/caller", "cached", "--token=" + token}, core.ContainerWithExecOpts{
					UseEntrypoint:            true,
					DaggerInDaggerNewSession: true,
				})
			}
		}
		tree := goGitBase(t, c).
			WithDirectory(".", serveTreeCachedModules(c)).
			WithNewFile("modules/caller/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "caller"`, 1)).
			WithNewFile("modules/caller/entrypoint/main.dang", serveTreeEntrypointCaller).
			WithoutFile("modules/caller/dagger.json")

		out, err := tree.With(newSessionCall(first)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"cached":"`+first+`\n"`)

		out, err = tree.
			WithDirectory("/elsewhere", tree.Directory("/work")).
			WithWorkdir("/elsewhere").
			WithoutDirectory("/work").
			With(newSessionCall(second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, second+`\n"`)
	})

	// The legacy two-checkout case with each CLI in a session of its own.
	t.Run("module cached from another checkout in another session serves from this checkout's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		manifest := "[modules.caller]\nsource = \"modules/caller\"\n"
		first, second := serveTreeToken(), serveTreeToken()
		newSessionCall := func(token string) core.WithContainerFunc {
			return func(ctr *core.Container) *core.Container {
				return ctr.WithExec([]string{"dagger", "call", "-m", "caller", "cached", "--address=/modules/hello", "--token=" + token}, core.ContainerWithExecOpts{
					UseEntrypoint:            true,
					DaggerInDaggerNewSession: true,
				})
			}
		}
		ctr := goGitBase(t, c).
			WithNewFile("/a/dagger.toml", manifest).
			WithDirectory("/a", serveTreeCachedModules(c)).
			WithNewFile("/b/dagger.toml", manifest).
			WithDirectory("/b", serveTreeCachedModules(c).WithNewFile("modules/hello/main.dang", serveTreeOtherHelloSource)).
			WithWorkdir("/a").
			WithExec([]string{"git", "init", "-q"}).
			WithWorkdir("/b").
			WithExec([]string{"git", "init", "-q"})

		ctr = ctr.WithWorkdir("/a").With(newSessionCall(first))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, first+"\n", out)

		out, err = ctr.WithWorkdir("/b").With(newSessionCall(second)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other "+second, out)
	})

	// The entrypoint module is a dependency, so no client serves it directly.
	t.Run("entrypoint module used as a dependency, cached from another host, serves from this host's tree", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		first, second := serveTreeToken(), serveTreeToken()
		tree := goGitBase(t, c).
			WithDirectory(".", serveTreeCachedModules(c)).
			WithNewFile("modules/caller/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "caller"`, 1)).
			WithNewFile("modules/caller/entrypoint/main.dang", serveTreeEntrypointCaller).
			WithoutFile("modules/caller/dagger.json").
			WithNewFile("modules/outer/dagger-module.toml", `name = "outer"
engineVersion = "latest"
source = "."

[runtime]
source = "dang"

[[dependencies]]
name = "caller"
source = "../caller"
`).
			WithNewFile("modules/outer/main.dang", `type Outer {
  pub cached(token: String!): String! {
    caller.cached(token: token)
  }
}
`)

		out, err := tree.
			With(daggerCallAt("./modules/outer", "cached", "--token="+first)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, first+`\n"`)

		out, err = tree.
			WithDirectory("/elsewhere", tree.Directory("/work")).
			WithWorkdir("/elsewhere").
			WithoutDirectory("/work").
			With(daggerCallAt("./modules/outer", "cached", "--token="+second)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, second+`\n"`)
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

// twoBranchRepo commits tree on main, and on next the same tree with one file
// changed.
func twoBranchRepo(c *dagger.Client, tree *core.Directory, path, contents string) *core.Directory {
	git := []string{"git", "-c", "user.email=dagger@example.com", "-c", "user.name=Dagger Tests"}
	return core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/repo", tree).
		WithWorkdir("/repo").
		WithExec([]string{"git", "init", "-q", "-b", "main"}).
		WithExec([]string{"git", "add", "-A"}).
		WithExec(append(slices.Clone(git), "commit", "-qm", "main")).
		WithExec([]string{"git", "checkout", "-qb", "next"}).
		WithNewFile(path, contents).
		WithExec([]string{"git", "add", "-A"}).
		WithExec(append(slices.Clone(git), "commit", "-qm", "next")).
		WithExec([]string{"git", "checkout", "-q", "main"}).
		Directory("/repo")
}

// A module serves modules from its own tree at run time, after the engine has
// looked up the calling function in the cache. Whatever the calling module
// can serve must therefore reach the function's cache key, or a change to a
// served module leaves the caller's result stale. Every subtest gets its own
// caller, so parallel subtests never hit each other's results.
func (ModuleLoadingSuite) TestServeModuleCallerTreeCache(ctx context.Context, t *testctx.T) {
	t.Run("git module follows a new commit of a sibling it serves", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		tree := serveTreeModules(c).
			WithNewFile("modules/caller/nonce.go", "package main\n\n// "+rand.Text()+"\n")
		gitDaemon, repoURL := gitService(ctx, t, c,
			twoBranchRepo(c, tree, "modules/hello/main.dang", serveTreeDecoySource))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n")

		out, err := ctr.With(daggerCallAt(repoURL+"#main:modules/caller", "message", "--address=/modules/hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)

		out, err = ctr.With(daggerCallAt(repoURL+"#next:modules/caller", "message", "--address=/modules/hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from the caller's workspace", out)
	})

	t.Run("git workspace module follows an edit to a sibling it serves", func(ctx context.Context, t *testctx.T) {
		tree := serveTreeModules(connect(ctx, t)).
			WithNewFile("modules/caller/nonce.go", "package main\n\n// "+rand.Text()+"\n")
		// message serves the sibling from the module's tree and returns its
		// answer. Each run is its own session.
		var commits []string
		message := func(edit bool) string {
			c := connect(ctx, t)
			gitDaemon, repoURL := gitService(ctx, t, c, tree)
			ref := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon}).Branch("main")
			commit, err := ref.CommitSHA(ctx)
			require.NoError(t, err)
			commits = append(commits, commit)
			ws := ref.AsWorkspace()
			if edit {
				// The same commit, with an edit the workspace carries.
				ws = ws.WithNewFile("modules/hello/main.dang", serveTreeDecoySource)
			}
			require.NoError(t, ws.ModuleSource("/modules/caller").AsModule().Serve(ctx))
			res, err := testutil.QueryWithClient[struct {
				Caller struct {
					Message string
				}
			}](c, t, `{caller{message(address: "/modules/hello")}}`, nil)
			require.NoError(t, err)
			return res.Caller.Message
		}
		require.Equal(t, "hi from hello", message(false))
		edited := message(true)
		require.Equal(t, commits[0], commits[1], "both runs serve the same commit")
		require.Equal(t, "hi from the caller's workspace", edited)
	})

	t.Run("git module in a clean repository workspace shares results with the same commit's ref", func(ctx context.Context, t *testctx.T) {
		// stamp changes on every run, so an equal answer is a shared result.
		tree := serveTreeModules(connect(ctx, t)).
			WithNewFile("modules/caller/nonce.go", "package main\n\n// "+rand.Text()+"\n").
			WithNewFile("modules/caller/stamp.go", `package main

import (
	"strconv"
	"time"
)

func (m *Caller) Stamp() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
`)
		stamp := func(asWorkspace func(*core.GitRepository) *core.Workspace) string {
			c := connect(ctx, t)
			gitDaemon, repoURL := gitService(ctx, t, c, tree)
			repo := core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon})
			require.NoError(t, asWorkspace(repo).ModuleSource("/modules/caller").AsModule().Serve(ctx))
			res, err := testutil.QueryWithClient[struct {
				Caller struct {
					Stamp string
				}
			}](c, t, `{caller{stamp}}`, nil)
			require.NoError(t, err)
			return res.Caller.Stamp
		}
		// The repository's own workspace applies its uncommitted changes, none
		// here; its commit's ref has none to apply.
		first := stamp(func(repo *core.GitRepository) *core.Workspace { return repo.AsWorkspace() })
		second := stamp(func(repo *core.GitRepository) *core.Workspace { return repo.Head().AsWorkspace() })
		require.Equal(t, first, second)
	})
}
