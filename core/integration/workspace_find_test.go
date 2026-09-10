package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestFindCLI(ctx context.Context, t *testctx.T) {
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
				ctr = ctr.WithDirectory("/selected", source.
					WithNewDirectory(".git").
					WithNewDirectory("empty"))
				workspace = "/selected/items"
			} else {
				ref := workspaceSelectionRemoteRef(ctx, t, c, source)
				workspace = strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
			}

			const entries = ".\n" +
				"./.hidden\n" +
				"./a.txt\n" +
				"./file with spaces.txt\n" +
				"./sub/\n" +
				"./sub/deep/\n" +
				"./sub/deep/nested.txt\n" +
				"./z.txt\n"
			for _, tc := range []struct {
				name string
				args []string
				want string
			}{
				{"default directory", nil, entries},
				{"explicit directory", []string{"."}, entries},
				{"nested directory", []string{"sub"}, "sub\nsub/deep/\nsub/deep/nested.txt\n"},
				{"root relative directory", []string{"/items/sub"}, "/items/sub\n/items/sub/deep/\n/items/sub/deep/nested.txt\n"},
				{"file", []string{"a.txt"}, "a.txt\n"},
				{"file with spaces", []string{"file with spaces.txt"}, "file with spaces.txt\n"},
				{"parent relative file", []string{"../root.txt"}, "../root.txt\n"},
				{"root relative file", []string{"/root.txt"}, "/root.txt\n"},
				{"multiple paths", []string{"a.txt", "sub", "/root.txt"}, "a.txt\nsub\nsub/deep/\nsub/deep/nested.txt\n/root.txt\n"},
				{"name filter", []string{"--name", "*.txt"}, "./a.txt\n./file with spaces.txt\n./sub/deep/nested.txt\n./z.txt\n"},
				{"repeatable name filter", []string{"--name", "*.txt", "--name", "sub"}, "./a.txt\n./file with spaces.txt\n./sub/\n./sub/deep/nested.txt\n./z.txt\n"},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					args := append([]string{"-W", workspace, "workspace", "find"}, tc.args...)
					out, err := ctr.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, out)
				})
			}

			t.Run("continue after missing path", func(ctx context.Context, t *testctx.T) {
				result := ctr.WithExec([]string{"dagger", "-W", workspace, "ws", "find", "missing", "a.txt", "sub"}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				})
				out, err := result.Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "a.txt\nsub\nsub/deep/\nsub/deep/nested.txt\n", out)
				stderr, err := result.Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, "no such file or directory")
			})

			if kind == "local" {
				t.Run("empty directory", func(ctx context.Context, t *testctx.T) {
					out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "workspace", "find", "/empty")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "/empty\n", out)
				})
			}
		})
	}
}
