package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// tsClientGitHost is the name the nested engine reaches the git fixture by:
// a git module source's host needs a dot.
const tsClientGitHost = "git.ts-client.test"

// A module's TypeScript client is generated from what the TypeScript SDK
// reads of the module source: the module's name and its dependencies, a git
// dependency's source and pin among them. Codegen's exec takes those as its
// args, so the exec is keyed by them rather than by the module source's ID,
// and a second client's generate of the unchanged module, in a session of
// its own, runs no codegen of its own.
func (GeneratorsSuite) TestTypeScriptClientFromModuleSource(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	gitDaemon, repoURL := gitService(ctx, t, outer, core.NewQuery(outer).Directory().
		WithNewFile("dagger-module.toml", `name = "wolfi"

[runtime]
source = "dang"
`).
		WithNewFile("main.dang", `type Wolfi {
  pub hello: String! { "hello" }
}
`))
	_, err := gitDaemon.Start(ctx)
	require.NoError(t, err)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	depRef := strings.Replace(repoURL, gitHost, tsClientGitHost, 1) + "#main"

	e := startClientEngine(ctx, t, outer, "ts-client", func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding(tsClientGitHost, gitDaemon)
	})

	checkout := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "dagger-module.toml"), []byte(`name = "App"
engineVersion = "latest"

[[dependencies]]
name = "wolfi"
source = "`+depRef+`"

[[clients]]
generator = "typescript"
directory = "client"
`), 0o644))

	// generate connects a fresh client, generates the module's client and
	// returns its bindings, the dependency's source and pin, and the
	// generate-client execs the engine's cache holds.
	type generated struct {
		bindings, depSource, pin string
		// execArgs is the args of each generate-client exec in the cache.
		execArgs []string
	}
	generate := func(t *testctx.T) generated {
		t.Helper()
		client := e.connect(ctx, t, checkout)
		defer func() { require.NoError(t, client.Close()) }()
		src := core.NewQuery(client).ModuleSource(".")
		var out generated
		out.bindings, err = src.GeneratedContextDirectory().File("client/client.gen.ts").Contents(ctx)
		require.NoError(t, err)
		deps, err := src.Dependencies(ctx)
		require.NoError(t, err)
		require.Len(t, deps, 1)
		out.depSource, err = deps[0].AsString(ctx)
		require.NoError(t, err)
		out.pin, err = deps[0].Pin(ctx)
		require.NoError(t, err)
		out.execArgs = e.generateClientExecs(ctx, t, outer)
		return out
	}

	first := generate(t)
	require.NotEmpty(t, first.pin)
	require.Contains(t, first.depSource, tsClientGitHost)
	require.Contains(t, first.bindings, `"`+first.depSource+`"`, "the client serves the git dependency from its source")
	require.Contains(t, first.bindings, `refPin: "`+first.pin+`"`, "at its pin")
	require.Contains(t, first.bindings, `.withName("wolfi")`, "under its name")
	require.Len(t, first.execArgs, 1, "the first client generated the client once")
	// The exec is keyed by what codegen reads, which its args carry, and
	// not by the module source's ID: a handle to a result of this engine.
	execArgs := first.execArgs[0]
	require.NotContains(t, execArgs, "module-source-id", "the exec's args hold no ID")
	for _, want := range []string{`"--module-name"`, `"App"`, `"--module-dependencies"`, first.pin} {
		require.Contains(t, execArgs, want, "the exec's args carry what codegen reads")
	}

	second := generate(t)
	require.Equal(t, first.bindings, second.bindings)
	require.Equal(t, first.execArgs, second.execArgs, "the second client's generate hit the first's: no new generate-client exec")
}
