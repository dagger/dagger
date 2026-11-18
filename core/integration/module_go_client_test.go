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

// goClientGitHost is the name the nested engine reaches the git fixture by:
// a git module source's host needs a dot.
const goClientGitHost = "git.go-client.test"

// A module's Go client is generated from what the Go SDK reads of the module
// source: the module's name, its engine version and its dependencies, a git
// dependency's source and pin among them. Codegen's exec takes those as its
// args, so the exec is keyed by them rather than by the module source's ID,
// and a second client's generate of the unchanged module, in a session of
// its own, runs no codegen of its own. A module with only clients and no
// dependencies may have no name, and its client still gets its engine
// version.
func (GeneratorsSuite) TestGoClientFromModuleSource(ctx context.Context, t *testctx.T) {
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
	depRef := strings.Replace(repoURL, gitHost, goClientGitHost, 1) + "#main"

	e := startClientEngine(ctx, t, outer, "go-client", func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding(goClientGitHost, gitDaemon)
	})

	// module is a checkout of a module with only a Go client, its config
	// starting with head.
	module := func(head string) string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger-module.toml"), []byte(head+`engineVersion = "latest"

[[clients]]
generator = "go"
directory = "client"
`), 0o644))
		return dir
	}
	checkout := module(`name = "App"

[[dependencies]]
name = "wolfi"
source = "` + depRef + `"
`)

	// generate connects a fresh client, generates the module's client and
	// returns its go.mod and its bindings, the module source's engine
	// version, the dependency's source and pin, and the generate-client
	// execs the engine's cache holds.
	type generated struct {
		goMod, bindings, engineVersion, depSource, pin string
		// execArgs is the args of each generate-client exec in the cache.
		execArgs []string
	}
	generate := func(t *testctx.T, checkout string) generated {
		t.Helper()
		client := e.connect(ctx, t, checkout)
		defer func() { require.NoError(t, client.Close()) }()
		src := core.NewQuery(client).ModuleSource(".")
		dir := src.GeneratedContextDirectory()
		var out generated
		out.goMod, err = dir.File("client/go.mod").Contents(ctx)
		require.NoError(t, err)
		out.bindings, err = dir.File("client/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)
		out.engineVersion, err = src.EngineVersion(ctx)
		require.NoError(t, err)
		deps, err := src.Dependencies(ctx)
		require.NoError(t, err)
		if len(deps) > 0 {
			require.Len(t, deps, 1)
			out.depSource, err = deps[0].AsString(ctx)
			require.NoError(t, err)
			out.pin, err = deps[0].Pin(ctx)
			require.NoError(t, err)
		}
		out.execArgs = e.generateClientExecs(ctx, t, outer)
		return out
	}

	first := generate(t, checkout)
	require.Contains(t, first.goMod, "module app/client\n", "the client's module path is the module's name, lowercased")
	require.NotEmpty(t, first.engineVersion)
	require.Contains(t, first.goMod, "dagger.io/dagger "+first.engineVersion, "the client requires dagger.io/dagger at the module's engine version")
	require.NotEmpty(t, first.pin)
	require.Contains(t, first.depSource, goClientGitHost)
	require.Contains(t, first.bindings, `"`+first.depSource+`"`, "the client serves the git dependency from its source")
	require.Contains(t, first.bindings, `RefPin: "`+first.pin+`"`, "at its pin")
	require.Contains(t, first.bindings, `WithName("wolfi")`, "under its name")
	require.Len(t, first.execArgs, 1, "the first client generated the client once")
	// The exec is keyed by what codegen reads, which its args carry, and
	// not by the module source's ID: a handle to a result of this engine.
	execArgs := first.execArgs[0]
	require.NotContains(t, execArgs, "module-source-id", "the exec's args hold no ID")
	for _, want := range []string{`"--module-name"`, `"App"`, `"--engine-version"`, `"` + first.engineVersion + `"`, `"--module-dependencies"`, first.pin} {
		require.Contains(t, execArgs, want, "the exec's args carry what codegen reads")
	}

	second := generate(t, checkout)
	require.Equal(t, first.goMod, second.goMod)
	require.Equal(t, first.bindings, second.bindings)
	require.Equal(t, first.execArgs, second.execArgs, "the second client's generate hit the first's: no new generate-client exec")

	// A module with only clients, and no dependencies, may have no name.
	// Its client still gets its engine version.
	unnamed := generate(t, module(""))
	require.Contains(t, unnamed.goMod, "module client\n", "the client's module path is its directory alone")
	require.NotEmpty(t, unnamed.engineVersion)
	require.Contains(t, unnamed.goMod, "dagger.io/dagger "+unnamed.engineVersion, "the unnamed module's client requires dagger.io/dagger at its engine version")
}
