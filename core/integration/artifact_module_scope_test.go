package core

import (
	"context"
	"slices"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ArtifactsSuite) TestCommandModuleLoadScope(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "generators-broken")
	listing := base.WithNewFile("/work/.dagger/modules/good/dagger-module.toml", `name = "good"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`).WithNewFile("/work/.dagger/modules/good/main.dang", `type Good {
  pub verify: Void @check { null }
  pub generate(source: Directory! @defaultPath(path: ".")): Changeset! @generate { source.changes(source) }
  pub web: Service! { container.from("nginx:alpine").asService }
  pub base: Container! { container.from("alpine") }
  pub assistant(base: LLM!): LLM! @agent { base }
}`)
	for _, command := range [][]string{{"check"}, {"up"}, {"shell"}, {"agent"}, {"generate", "--require-load"}} {
		t.Run(strings.Join(command, " "), func(ctx context.Context, t *testctx.T) {
			for _, selector := range []string{"--good", "--module=good", "dag://?module=good"} {
				args := append(slices.Clone(command), "-l", "-f=link", selector)
				out, err := listing.With(daggerExec(args...)).Stdout(ctx)
				require.NoError(t, err, strings.Join(args, " "))
				require.Contains(t, out, "://good/")
				require.NotContains(t, out, "bad/load")
			}
			if command[0] != "check" {
				args := append(slices.Clone(command), "-l", "--module=bad")
				out, err := listing.With(daggerExecFail(args...)).CombinedOutput(ctx)
				require.NoError(t, err, out)
				require.Contains(t, out, "workspace modules could not be loaded")
				require.Contains(t, out, "bad")
			}
		})
	}
	t.Run("check execution preserves selected load failures", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "--good", "--generated=false")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		out, err = base.With(daggerExecFail("check", "--module=bad", "--generated=false", "--progress=report")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad/load")
	})
	t.Run("a type filter does not hide a selected load failure", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExecFail("up", "-l", "dag+service://?module=bad")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "workspace modules could not be loaded")
	})
	for _, selector := range []string{"dag://?module=good", "--module=good"} {
		t.Run(selector+" avoids loading unrelated modules", func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerExec("check", selector, "--generated=false", "--progress=report", "-vv")).CombinedOutput(ctx)
			require.NoError(t, err, out)
			require.NotContains(t, out, "intentionally invalid")
			require.NotContains(t, out, "modules/bad")
		})
	}
}
