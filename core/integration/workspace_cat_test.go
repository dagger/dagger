package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestCatCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("root.txt", "from the workspace root\n").
		WithNewFile("items/message.txt", "hello\r\nworld\r\n").
		WithNewFile("items/no-newline.txt", "no final newline").
		WithNewFile("items/empty.txt", "").
		WithNewFile("items/file with spaces.txt", "こんにちは\n")
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/caller/message.txt", "from the caller").
		WithWorkdir("/caller")

	for _, kind := range []string{"local", "remote"} {
		t.Run(kind, func(ctx context.Context, t *testctx.T) {
			ctr := base
			var workspace string
			if kind == "local" {
				ctr = ctr.WithDirectory("/selected", source.WithNewDirectory(".git"))
				workspace = "/selected/items"
			} else {
				ref := workspaceSelectionRemoteRef(ctx, t, c, source)
				workspace = strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
			}

			for _, tc := range []struct {
				path string
				want string
			}{
				{"message.txt", "hello\r\nworld\r\n"},
				{"/items/message.txt", "hello\r\nworld\r\n"},
				{"no-newline.txt", "no final newline"},
				{"empty.txt", ""},
				{"file with spaces.txt", "こんにちは\n"},
				{"../root.txt", "from the workspace root\n"},
				{"/root.txt", "from the workspace root\n"},
			} {
				t.Run(tc.path, func(ctx context.Context, t *testctx.T) {
					out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "workspace", "cat", tc.path)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, out)
				})
			}

			for _, target := range []string{"missing", "."} {
				t.Run("invalid file "+target, func(ctx context.Context, t *testctx.T) {
					result := ctr.WithExec([]string{"dagger", "-W", workspace, "ws", "cat", target}, dagger.ContainerWithExecOpts{
						ExperimentalPrivilegedNesting: true,
						Expect:                        dagger.ReturnTypeFailure,
					})
					out, err := result.Stdout(ctx)
					require.NoError(t, err)
					require.Empty(t, out)
					stderr, err := result.Stderr(ctx)
					require.NoError(t, err)
					if target == "." {
						require.Contains(t, stderr, "is a directory")
					} else {
						require.Contains(t, stderr, "no such file or directory")
					}
				})
			}

			t.Run("multiple files", func(ctx context.Context, t *testctx.T) {
				out, err := ctr.With(workspaceSelectionDaggerExec("-W", workspace, "ws", "cat", "no-newline.txt", "empty.txt", "message.txt")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "no final newlinehello\r\nworld\r\n", out)
			})

			t.Run("continue after missing file", func(ctx context.Context, t *testctx.T) {
				result := ctr.WithExec([]string{"dagger", "-W", workspace, "ws", "cat", "no-newline.txt", "missing", "/root.txt"}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				})
				out, err := result.Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "no final newlinefrom the workspace root\n", out)
				stderr, err := result.Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, "no such file or directory")
			})
		})
	}
}
