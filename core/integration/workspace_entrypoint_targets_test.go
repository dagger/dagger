package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestEntrypointTargetNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceBase(t, c).WithNewFile("dagger.toml", `
[modules.app]
source = "app"
entrypoint = true

[modules.other]
source = "other"
`)
	for _, name := range []string{"app", "other"} {
		verify := "null"
		if name == "other" {
			verify = `raise "the other module must be selected explicitly"`
		}
		base = base.
			WithNewFile(name+"/dagger-module.toml", fmt.Sprintf("name = %q\nengineVersion = \"latest\"\n[runtime]\nsource = \"dang\"\n", name)).
			WithNewFile(name+"/main.dang", fmt.Sprintf(`type %s {
  pub verify: Void @check { %s }
  pub files(ws: Workspace!): Changeset! @generate {
    ws.withNewFile(%q, "generated").changes(ws)
  }
  pub dev: Container! { container.from("alpine:3.22") }
  pub web: Service! @up {
    container.from("nginx:alpine").withExposedPort(80).asService
  }
}
`, strings.ToUpper(name[:1])+name[1:], verify, name+".txt"))
	}
	base = base.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))
	config, err := base.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)

	for _, test := range []struct{ command, target string }{
		{"check", "verify"},
		{"generate", "files"},
		{"shell", "dev"},
		{"up", "web"},
	} {
		t.Run(test.command, func(ctx context.Context, t *testctx.T) {
			for _, selection := range []struct {
				args []string
				want []string
			}{
				{want: []string{test.target, "other:" + test.target}},
				{args: []string{test.target}, want: []string{test.target}},
				{args: []string{"app:" + test.target}, want: []string{test.target}},
				{args: []string{"other:" + test.target}, want: []string{"other:" + test.target}},
				{args: []string{"*:" + test.target}, want: []string{test.target, "other:" + test.target}},
			} {
				args := append([]string{test.command, "-l"}, selection.args...)
				if test.command == "check" {
					args = append(args, "--no-generate")
				}
				out, err := base.With(daggerNonNestedExec(args...)).Stdout(ctx)
				require.NoError(t, err, strings.Join(args, " "))
				var names []string
				for line := range strings.SplitSeq(out, "\n") {
					fields := strings.Fields(line)
					if len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
						names = append(names, fields[0])
					}
				}
				require.ElementsMatch(t, selection.want, names, strings.Join(args, " "))
			}

			ordinary := base.WithNewFile("dagger.toml", strings.Replace(config, "entrypoint = true", "entrypoint = false", 1))
			out, err := ordinary.With(daggerNonNestedExec(test.command, "-l", test.target)).Stdout(ctx)
			require.NoError(t, err)
			require.NotContains(t, out, test.target)

			out, err = ordinary.With(daggerNonNestedExec("-m", "./app", test.command, "-l", test.target)).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, test.target)
			require.NotContains(t, out, "app:"+test.target)
			require.NotContains(t, out, "other:"+test.target)
		})
	}

	t.Run("caller skip only excludes the entrypoint", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerNonNestedExec("check", "-l", "--no-generate", "--skip=verify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other:verify", strings.TrimSpace(out))
	})

	t.Run("module settings keep local skip names", func(ctx context.Context, t *testctx.T) {
		out, err := base.WithNewFile("dagger.toml", config+"\ncheck.skip = [\"verify\"]\n").
			With(daggerNonNestedExec("check", "-l", "--no-generate")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "verify", strings.TrimSpace(out))
	})

	t.Run("value workspace uses its own entrypoint", func(ctx context.Context, t *testctx.T) {
		ws := base.Directory("/work").AsWorkspace()
		checks, err := ws.Checks(dagger.WorkspaceChecksOpts{Include: []string{"verify"}, NoGenerate: new(true)}).List(ctx)
		require.NoError(t, err)
		require.Len(t, checks, 1)
		name, err := checks[0].Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "verify", name)

		changed := ws.WithNewFile("dagger.toml", strings.Replace(config, "entrypoint = true", "entrypoint = false", 1))
		checks, err = changed.Checks(dagger.WorkspaceChecksOpts{Include: []string{"verify"}, NoGenerate: new(true)}).List(ctx)
		require.NoError(t, err)
		require.Empty(t, checks)
	})

	t.Run("run the entrypoint generator only", func(ctx context.Context, t *testctx.T) {
		generated := base.With(daggerNonNestedExec("generate", "files", "-y"))
		out, err := generated.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "files")
		require.NotContains(t, out, "app:files")
		exists, err := generated.Exists(ctx, "app.txt")
		require.NoError(t, err)
		require.True(t, exists)
		exists, err = generated.Exists(ctx, "other.txt")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("run the entrypoint check only", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerNonNestedExec("check", "verify", "--no-generate")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "app:verify")
		require.NotContains(t, out, "other:verify")
	})
}
