package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestLsCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("root.txt", "root").
		WithNewFile("items/.hidden", "hidden").
		WithNewFile("items/a.txt", "a").
		WithNewFile("items/file with spaces.txt", "spaces").
		WithNewFile("items/sub/deep/nested.txt", "nested").
		WithNewFile("items/z.txt", "z")
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/caller/local-only.txt", "caller").
		WithWorkdir("/caller")

	for _, kind := range []string{"local", "remote"} {
		t.Run(kind, func(ctx context.Context, t *testctx.T) {
			ctr := base
			var workspace string
			if kind == "local" {
				ctr = ctr.WithDirectory("/selected", source).
					WithNewDirectory("/selected/.git").
					WithNewDirectory("/selected/empty")
				workspace = "/selected/items"
			} else {
				ref := workspaceSelectionRemoteRef(ctx, t, c, source)
				workspace = strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
			}

			const entries = ".hidden\na.txt\nfile with spaces.txt\nsub/\nz.txt\n"
			for _, tc := range []struct {
				name string
				args []string
				want string
			}{
				{"default directory", nil, entries},
				{"explicit directory", []string{"."}, entries},
				{"root relative directory", []string{"/items"}, entries},
				{"nested directory", []string{"sub/"}, "deep/\n"},
				{"file", []string{"a.txt"}, "a.txt\n"},
				{"nested file", []string{"sub/deep/nested.txt"}, "sub/deep/nested.txt\n"},
				{"file with spaces", []string{"file with spaces.txt"}, "file with spaces.txt\n"},
				{"parent relative file", []string{"../root.txt"}, "../root.txt\n"},
				{"root relative file", []string{"/root.txt"}, "/root.txt\n"},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"-W", workspace, "workspace", "ls"}, tc.args...)
					out, err := ctr.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, out)
				})
			}

			for _, target := range []string{"/", ".."} {
				t.Run("workspace root "+target, func(ctx context.Context, t *testctx.T) {
					out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "ws", "ls", target)).Stdout(ctx)
					require.NoError(t, err)
					require.Contains(t, out, "items/\n")
					require.Contains(t, out, "root.txt\n")
					require.NotContains(t, out, "local-only.txt")
				})
			}

			t.Run("missing path", func(ctx context.Context, t *testctx.T) {
				result := ctr.WithExec([]string{"dagger", "-W", workspace, "workspace", "ls", "missing"}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				})
				out, err := result.Stdout(ctx)
				require.NoError(t, err)
				require.Empty(t, out)
				stderr, err := result.Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, `list workspace path "missing"`)
			})

			if kind == "local" {
				t.Run("empty directory", func(ctx context.Context, t *testctx.T) {
					out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "workspace", "ls", "/empty")).Stdout(ctx)
					require.NoError(t, err)
					require.Empty(t, out)
				})
			}
		})
	}
}
