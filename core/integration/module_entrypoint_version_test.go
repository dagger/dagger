package core

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// entrypointVersionModule is a workspace with module tiny, whose entrypoint is
// entrypoint/main.dang with source, plus extra files under the module
// directory, such as a dang.toml. A path starting with / is at the workspace
// root instead.
func entrypointVersionModule(t *testctx.T, c *dagger.Client, source string, files map[string]string) *core.Container {
	t.Helper()
	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.tiny]\nsource = \"tiny\"\n").
		WithNewFile("tiny/dagger-module.toml", "name = \"tiny\"\n\n[entrypoint]\nkind = \"dang\"\nsource = \"./entrypoint\"\n").
		WithNewFile("tiny/entrypoint/main.dang", source)
	for path, contents := range files {
		if rootPath, ok := strings.CutPrefix(path, "/"); ok {
			ctr = ctr.WithNewFile(rootPath, contents)
			continue
		}
		ctr = ctr.WithNewFile("tiny/"+path, contents)
	}
	return ctr
}

// entrypointEngineVersion is the engine version the engine loads module tiny
// at.
func entrypointEngineVersion(ctx context.Context, t *testctx.T, ctr *core.Container) (string, error) {
	t.Helper()
	out, err := ctr.
		With(daggerExec("core", "module-source", "--ref-string", "tiny", "engine-version")).
		Stdout(ctx)
	return strings.TrimSpace(out), err
}

// helloEntrypointSource is testdata/modules/dang/module-entrypoint/main.dang
// with extra members on the entrypoint type, for source that must type check
// or parse at a version.
func helloEntrypointSource(extra string) string {
	return `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Tiny")
        .withConstructor(function("", typeDef.withObject("Tiny")))
        .withFunction(function("Hello", typeDef.withKind(TypeDefKind.STRING_KIND))),
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
      ("\"hello\"" :: JSON!)
    }
  }
` + extra + `}
`
}

// A manifest v2 module declares its engine version in the dang.toml that
// governs its entrypoint, as `[imports.Dagger] dagger = "..."`. Without one, it
// is v1.0.0-beta.20, the last release before the naming cut, whatever engine
// loads it.
func (ModuleSuite) TestDangModuleEntrypointEngineVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := helloEntrypointSource("")

	for _, tc := range []struct {
		name    string
		files   map[string]string
		version string
	}{
		{
			name:    "no dang.toml",
			version: "v1.0.0-beta.20",
		},
		{
			name:    "dang.toml without a Dagger import",
			files:   map[string]string{"entrypoint/dang.toml": "dang = \"2.1\"\n"},
			version: "v1.0.0-beta.20",
		},
		{
			name:    "dagger = true",
			files:   map[string]string{"entrypoint/dang.toml": "[imports.Dagger]\ndagger = true\n"},
			version: "v1.0.0-beta.20",
		},
		{
			name:    "declared in the entrypoint directory",
			files:   map[string]string{"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v0.20.1\"\n"},
			version: "v0.20.1",
		},
		{
			name:    "declared prerelease",
			files:   map[string]string{"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v1.0.0-beta.19\"\n"},
			version: "v1.0.0-beta.19",
		},
		{
			name:    "declared above the entrypoint directory",
			files:   map[string]string{"dang.toml": "[imports.Dagger]\ndagger = \"0.20\"\n"},
			version: "v0.20.0",
		},
		{
			// It would govern the entrypoint by Dang's rules, but loading the
			// module another way could leave it out of the module context.
			name:    "a dang.toml above the module directory does not count",
			files:   map[string]string{"/dang.toml": "[imports.Dagger]\ndagger = \"v0.20.0\"\n"},
			version: "v1.0.0-beta.20",
		},
		{
			name: "the nearest dang.toml decides",
			files: map[string]string{
				"dang.toml":            "[imports.Dagger]\ndagger = \"v0.20.0\"\n",
				"entrypoint/dang.toml": "dang = \"2.1\"\n",
			},
			version: "v1.0.0-beta.20",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			version, err := entrypointEngineVersion(ctx, t, entrypointVersionModule(t, c, source, tc.files))
			require.NoError(t, err)
			require.Equal(t, tc.version, version)
		})
	}

	t.Run("directory source ignores a dang.toml above the module", func(ctx context.Context, t *testctx.T) {
		version, err := core.NewQuery(c).Directory().
			WithNewFile("dang.toml", "[imports.Dagger]\ndagger = \"v0.20.0\"\n").
			WithNewFile("tiny/dagger-module.toml", "name = \"tiny\"\n\n[entrypoint]\nkind = \"dang\"\nsource = \"./entrypoint\"\n").
			WithNewFile("tiny/entrypoint/main.dang", source).
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "tiny"}).
			EngineVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta.20", version)
	})

	t.Run("module reference", func(ctx context.Context, t *testctx.T) {
		served := core.NewQuery(c).Directory().
			WithNewFile("entrypoint/main.dang", source).
			WithNewFile("entrypoint/dang.toml", "[imports.Dagger]\ndagger = \"v0.20.1\"\n")
		gitDaemon, repoURL := gitService(ctx, t, c, served)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		version, err := entrypointEngineVersion(ctx, t, goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("tiny/dagger-module.toml", "name = \"tiny\"\n\n[entrypoint]\nkind = \"dang\"\nsource = \""+repoURL+"#main:entrypoint\"\n"))
		require.NoError(t, err)
		require.Equal(t, "v0.20.1", version)
	})
}

// The declared version selects the core API the entrypoint is type checked
// against, as engineVersion does for a module with a runtime:
// Workspace.resolve exists from v1.0.0 on.
func (ModuleSuite) TestDangModuleEntrypointDeclaredAPIView(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := helloEntrypointSource(`
  pub probe(workspace: Workspace!): String! {
    workspace.resolve("alpine").value
  }
`)

	t.Run("default view", func(ctx context.Context, t *testctx.T) {
		out, err := entrypointVersionModule(t, c, source, nil).
			With(daggerCallAt("tiny", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})

	t.Run("declared older view", func(ctx context.Context, t *testctx.T) {
		_, err := entrypointVersionModule(t, c, source, map[string]string{
			"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v0.20.0\"\n",
		}).
			With(daggerCallAt("tiny", "hello")).
			Sync(ctx)
		requireErrOut(t, err, `"resolve" not found`)
	})
}

// sources runs while the module source loads, with no module to serve its
// client at the module's view. It must still run at the declared view, the
// one it was type checked against: Workspace.path exists only before v1.0.0.
func (ModuleSuite) TestDangModuleEntrypointSourcesAtDeclaredView(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := `type Entrypoint implements ModuleEntrypoint {
  pub sources(workspace: Workspace!): Directory! {
    workspace.directory("/", include: ["tiny/**"]).withNewFile("tiny/workspace-path", workspace.path)
  }

  pub types: [TypeDef!]! {
    [
      typeDef
        .withObject("Tiny")
        .withConstructor(function("", typeDef.withObject("Tiny")))
        .withFunction(function("Hello", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      ("\"hello\"" :: JSON!)
    }
  }
}
`
	out, err := entrypointVersionModule(t, c, source, map[string]string{
		"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v0.20.1\"\n",
	}).
		With(daggerCallAt("tiny", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (ModuleSuite) TestDangModuleEntrypointRequiresNewerEngine(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := entrypointVersionModule(t, c, helloEntrypointSource(""), map[string]string{
		"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v999.0.0\"\n",
	})

	_, err := ctr.With(daggerCallAt("tiny", "hello")).Sync(ctx)
	requireErrOut(t, err, "module requires dagger v999.0.0, but you have")

	_, err = entrypointEngineVersion(ctx, t, ctr)
	requireErrOut(t, err, "module requires dagger v999.0.0, but you have")
}

// The entrypoint is parsed as the Dang version its dang.toml declares. Dang
// 2.0 separates top-level forms with commas; Dang 2.1, the default, does not.
func (ModuleSuite) TestDangModuleEntrypointDangVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := helloEntrypointSource("") + "let legacyA = 1, let legacyB = 2\n"

	t.Run("declared", func(ctx context.Context, t *testctx.T) {
		out, err := entrypointVersionModule(t, c, source, map[string]string{
			"entrypoint/dang.toml": "dang = \"2.0\"\n",
		}).
			With(daggerCallAt("tiny", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})

	t.Run("undeclared", func(ctx context.Context, t *testctx.T) {
		_, err := entrypointVersionModule(t, c, source, map[string]string{
			"entrypoint/dang.toml": "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n",
		}).
			With(daggerCallAt("tiny", "hello")).
			Sync(ctx)
		require.Error(t, err)
	})

	t.Run("too new", func(ctx context.Context, t *testctx.T) {
		_, err := entrypointVersionModule(t, c, helloEntrypointSource(""), map[string]string{
			"entrypoint/dang.toml": "dang = \"99.0\"\n",
		}).
			With(daggerCallAt("tiny", "hello")).
			Sync(ctx)
		requireErrOut(t, err, "this project needs Dang 99.0")
	})
}

// Entrypoints generated before the version could be declared spell cache
// policies as the API did before the naming cut (FunctionCachePolicy.Never,
// .PerSession, .Default). Loaded with no declared version, they keep that API.
func (ModuleSuite) TestDangModuleEntrypointVersionlessCachePolicies(ctx context.Context, t *testctx.T) {
	runID := rand.Text()
	c := connect(ctx, t)
	mod := nonceModule(c, runID).AsModuleSource()

	version, err := mod.EngineVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, "v1.0.0-beta.20", version)

	c = serveNonceModule(ctx, t, runID)
	require.NotEqual(t, queryNonce(t, c, "never"), queryNonce(t, c, "never"))
	session := queryNonce(t, c, "session")
	require.Equal(t, session, queryNonce(t, c, "session"))
}
