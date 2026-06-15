package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestSyntheticWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().
		WithNewFile("hello.txt", "hello from synthetic workspace").
		WithNewFile("a/target.txt", "found").
		WithNewFile("a/b/leaf.txt", "leaf")

	ws := dir.AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/a/b",
	})

	address, err := ws.Address(ctx)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(address, "directory://"))

	cwd, err := ws.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/a/b", cwd)

	contents, err := ws.File("/hello.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from synthetic workspace", contents)

	leaf, err := ws.File("leaf.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "leaf", leaf)

	found, err := ws.FindUp(ctx, "target.txt")
	require.NoError(t, err)
	require.Equal(t, "/a/target.txt", found)

	entries, err := ws.Directory("..").Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "target.txt")
	hasBDir := false
	for _, entry := range entries {
		if entry == "b" || entry == "b/" {
			hasBDir = true
			break
		}
	}
	require.Truef(t, hasBDir, "entries: %v", entries)

	_, err = ws.Directory(".", dagger.WorkspaceDirectoryOpts{Gitignore: true}).ID(ctx)
	require.ErrorContains(t, err, "gitignore filtering is only supported for local workspaces")

	emptyWS := c.Directory().AsWorkspace()
	emptyCwd, err := emptyWS.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/", emptyCwd)

	emptyEntries, err := emptyWS.Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, emptyEntries)

	for _, tc := range []struct {
		name string
		run  func(context.Context, *testctx.T)
	}{
		{
			name: "checks",
			run: func(ctx context.Context, t *testctx.T) {
				checks, err := ws.Checks().List(ctx)
				require.NoError(t, err)
				require.Empty(t, checks)
			},
		},
		{
			name: "generators",
			run: func(ctx context.Context, t *testctx.T) {
				generators, err := ws.Generators().List(ctx)
				require.NoError(t, err)
				require.Empty(t, generators)
			},
		},
		{
			name: "services",
			run: func(ctx context.Context, t *testctx.T) {
				services, err := ws.Services().List(ctx)
				require.NoError(t, err)
				require.Empty(t, services)
			},
		},
		{
			name: "module list",
			run: func(ctx context.Context, t *testctx.T) {
				modules, err := ws.ModuleList(ctx)
				require.NoError(t, err)
				require.Empty(t, modules)
			},
		},
		{
			name: "env list",
			run: func(ctx context.Context, t *testctx.T) {
				envs, err := ws.EnvList(ctx)
				require.NoError(t, err)
				require.Empty(t, envs)
			},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			tc.run(ctx, t)
		})
	}

	for _, tc := range []struct {
		name string
		run  func(context.Context) error
		err  string
	}{
		{
			name: "config read",
			run: func(ctx context.Context) error {
				_, err := ws.ConfigRead(ctx)
				return err
			},
			err: `workspace feature "config" is not supported for synthetic/rootfs-backed workspaces`,
		},
		{
			name: "install",
			run: func(ctx context.Context) error {
				_, err := ws.Install(ctx, "github.com/dagger/dagger/modules/wolfi")
				return err
			},
			err: `workspace feature "module installation" is not supported for synthetic/rootfs-backed workspaces`,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			err := tc.run(ctx)
			require.ErrorContains(t, err, tc.err)
		})
	}
}
