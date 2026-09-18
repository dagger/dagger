package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A single generator must not round-trip its snapshots through Git: Git only
// records the executable bit and does not record empty directories.
func (GeneratorsSuite) TestSingleGeneratorPreservesSnapshots(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "generators-broken").
		WithNewFile("dagger.toml", "[modules.good]\nsource = \".dagger/modules/good\"\n").
		WithNewFile(".dagger/modules/good/main.dang", `type Good {
  pub generate(workdir: Directory! @defaultPath(path: "/")): Changeset! @generate {
    let private = directory
      .withNewFile("text", "updated", permissions: 384)
      .withNewFile("mode-only", "same", permissions: 384)
    workdir
      .withoutFile("out/old")
      .withFile("out/app", workdir.file("payload"), permissions: 488)
      .withFile("out/text", private.file("text"))
      .withFile("out/mode-only", private.file("mode-only"))
      .withNewDirectory("out/empty", permissions: 448)
      .changes(workdir)
  }
}`).
		WithNewFile("payload", "binary\x00payload\n").
		WithNewFile("out/old", "removed").
		WithNewFile("out/text", "original").
		WithNewFile("out/mode-only", "same").
		WithNewFile("out/untouched", "unchanged").
		WithExec([]string{"chmod", "0700", "out"}).
		WithExec([]string{"chmod", "0664", "out/untouched"})

	generated := base.With(daggerExec("generate", "-y"))
	out, err := generated.CombinedOutput(ctx)
	require.NoError(t, err, out)
	// Exercise another standalone CLI session too: a no-op rerun must not
	// widen existing permissions or remove an empty output directory.
	for _, result := range []*dagger.Container{generated, generated.With(daggerExec("generate", "-y"))} {
		contents, err := result.File("out/app").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "binary\x00payload\n", contents)
		contents, err = result.File("out/text").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "updated", contents)
		contents, err = result.File("out/untouched").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "unchanged", contents)
		contents, err = result.File("out/mode-only").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "same", contents)
		exists, err := result.Exists(ctx, "out/old")
		require.NoError(t, err)
		require.False(t, exists)
		modes, err := result.WithExec([]string{"stat", "-c", "%a", "out", "out/app", "out/text", "out/empty", "out/untouched", "out/mode-only"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "700\n750\n600\n700\n664\n600\n", modes)
	}
}

func (GeneratorsSuite) TestSingleGeneratorExcludesGitMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name, before, after string
	}{
		{
			name:   "directory",
			before: `directory.withNewFile(".git/removed", "keep").withNewFile(".git/modified", "original")`,
			after:  `before.withoutFile(".git/removed").withNewFile(".git/modified", "changed").withNewFile(".git/added", "ignored")`,
		},
		{
			name:   "worktree file",
			before: `directory.withNewFile(".git", "gitdir: original")`,
			after:  `before.withNewFile(".git", "gitdir: changed")`,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			for _, extra := range []string{"", `.withNewFile("generated.txt", "generated")`} {
				base := workspaceFixture(t, c, "generators-broken").
					WithNewFile("dagger.toml", "[modules.good]\nsource = \".dagger/modules/good\"\n").
					WithNewFile(".git/removed", "keep").
					WithNewFile(".git/modified", "original").
					WithNewFile(".dagger/modules/good/main.dang", `type Good {
  pub generate: Changeset! @generate {
    let before = `+tc.before+`
    `+tc.after+extra+`.changes(before)
  }
}`)
				out, err := base.With(daggerQuery(`{
  currentWorkspace { generators { run { changes {
    before { exists(path: ".git", doNotFollowSymlinks: true) }
    after { exists(path: ".git", doNotFollowSymlinks: true) }
  } } } }
}`)).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"currentWorkspace":{"generators":{"run":{"changes":{"before":{"exists":false},"after":{"exists":false}}}}}}`, out)
				generated := base.With(daggerExec("generate", "-y"))
				out, err = generated.CombinedOutput(ctx)
				require.NoError(t, err, out)
				for path, expected := range map[string]string{".git/removed": "keep", ".git/modified": "original"} {
					contents, err := generated.File(path).Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, expected, contents)
				}
				exists, err := generated.Exists(ctx, ".git/added")
				require.NoError(t, err)
				require.False(t, exists)
				exists, err = generated.Exists(ctx, "generated.txt")
				require.NoError(t, err)
				require.Equal(t, extra != "", exists)
			}
		})
	}
}

func (GeneratorsSuite) TestSingleGeneratorVerifiesSkippedModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "generators-broken").
		WithNewFile(".dagger/modules/good/main.dang", `type Good {
  pub generate(workdir: Directory! @defaultPath(path: "/")): Changeset! @generate {
    workdir.withNewFile(".dagger/modules/bad/main.dang", "type Bad { pub hello: String! { \"repaired\" } }\n").changes(workdir)
  }
}`)
	generated := base.With(daggerExec("generate", "-y"))
	out, err := generated.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "REGENERATED")
	require.Contains(t, out, "could not load before this run's changes; loads with them applied")
	contents, err := generated.File(".dagger/modules/bad/main.dang").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, "repaired")
	out, err = generated.With(daggerExec("call", "bad", "hello")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "repaired", strings.TrimSpace(out))
}
