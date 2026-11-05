package core

import (
	"context"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// includeProbeEntrypoint is a module entrypoint without a sources member, as
// the Go SDK generates one whose build reads files outside the module
// directory: the manifest's include names them, and types and call take the
// workspace they don't read. Nonce returns the shared file and a random
// string, so it repeats exactly when the engine reuses the call result.
const includeProbeEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("ContextFiles", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Shared", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Nonce", typeDef.withKind(TypeDefKind.STRING_KIND))),
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
    } else if (fnName == "ContextFiles") {
      (JSON.encode(currentModule.contextDirectory.glob("**").join("\n")) :: JSON!)
    } else if (fnName == "Shared") {
      (JSON.encode(shared) :: JSON!)
    } else if (fnName == "Nonce") {
      (JSON.encode(shared.trimSpace + "@" + Random.string) :: JSON!)
    } else {
      raise "unknown function: " + fnName
    }
  }

  let shared: String! {
    currentModule.contextDirectory.file(currentModule.sourceSubpath + "/../shared/value.txt").contents
  }
}
`

const includeModuleManifest = `name = "m"
include = ["../shared/*.txt", "!../shared/skip.txt"]

[entrypoint]
kind = "dang"
source = "./entrypoint"
`

func includeModuleFixture(c *dagger.Client) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("shared/value.txt", "from the shared sibling\n").
		WithNewFile("shared/skip.txt", "excluded\n").
		WithNewFile("other/unlisted.txt", "unlisted\n").
		WithNewFile("m/dagger-module.toml", includeModuleManifest).
		WithNewFile("m/data/local.txt", "module data\n").
		WithNewFile("m/entrypoint/main.dang", includeProbeEntrypoint)
}

// TestModuleEntrypointInclude covers a manifest version 2 include: the module
// builds from the files it names outside the module directory, and its
// results follow them.
func (ModuleSuite) TestModuleEntrypointInclude(ctx context.Context, t *testctx.T) {
	call := func(ctx context.Context, t *testctx.T, ctr *core.Container, ref, fn string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(ref, fn)).Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	requireLoaded := func(t *testctx.T, files []string) {
		t.Helper()
		require.Contains(t, files, "m/dagger-module.toml")
		require.Contains(t, files, "m/entrypoint/main.dang")
		require.Contains(t, files, "m/data/local.txt")
		require.Contains(t, files, "shared/value.txt")
		require.NotContains(t, files, "shared/skip.txt")
		require.NotContains(t, files, "other/unlisted.txt")
		requireNoGitMetadata(t, files)
	}

	t.Run("local module loads the included files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).WithDirectory(".", includeModuleFixture(c))

		files, err := moduleSourceContextFiles(ctx, ctr, "./m")
		require.NoError(t, err)
		requireLoaded(t, files)
		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "./m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "./m", "shared"))
	})

	t.Run("git module loads the included files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		gitDaemon, repoURL := gitService(ctx, t, c, includeModuleFixture(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("shared/value.txt", "the caller's copy\n")

		files, err := moduleSourceContextFiles(ctx, ctr, repoURL+"#main:m")
		require.NoError(t, err)
		requireLoaded(t, files)
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, repoURL+"#main:m", "shared"))
	})

	// A cached result answers again only while the included files are the
	// same: changing one makes the call run again.
	t.Run("results follow an included file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).WithDirectory(".", includeModuleFixture(c))

		first := call(ctx, t, ctr, "./m", "nonce")
		require.True(t, strings.HasPrefix(first, "from the shared sibling@"), first)
		require.Equal(t, first, call(ctx, t, ctr, "./m", "nonce"))

		changed := ctr.WithNewFile("shared/value.txt", "changed\n")
		after := call(ctx, t, changed, "./m", "nonce")
		require.True(t, strings.HasPrefix(after, "changed@"), after)

		// A file the include excludes is not part of the module.
		skipped := ctr.WithNewFile("shared/skip.txt", "changed too\n")
		require.Equal(t, first, call(ctx, t, skipped, "./m", "nonce"))
	})
}
