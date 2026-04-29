package core

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestCurrentWorkspaceRootAndCwd(ctx context.Context, t *testctx.T) {
	fixture := newWorkspaceCwdFixture(t)

	for _, tc := range []struct {
		name        string
		clientOpts  []dagger.ClientOpt
		wantCwdPath string
	}{
		{
			name:        "detection starts at workspace root",
			clientOpts:  []dagger.ClientOpt{dagger.WithWorkdir(fixture.root)},
			wantCwdPath: "",
		},
		{
			name:        "detection starts below workspace root",
			clientOpts:  []dagger.ClientOpt{dagger.WithWorkdir(fixture.cwd)},
			wantCwdPath: "subdir",
		},
		{
			name:        "explicit local workspace starts below workspace root",
			clientOpts:  []dagger.ClientOpt{dagger.WithWorkspace(fixture.cwd)},
			wantCwdPath: "subdir",
		},
	} {
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			t.Run("workspace root", func(ctx context.Context, t *testctx.T) {
				res, err := testutil.Query[currentWorkspaceRootResult](t, `{
					currentWorkspace {
						path
						rootFile: file(path: "root.txt") {
							contents
						}
						rootDirectory: directory(path: ".") {
							entries
						}
					}
				}`, nil, tc.clientOpts...)
				require.NoError(t, err)

				require.Equal(t, ".", res.CurrentWorkspace.Path)
				require.Equal(t, "from workspace root", res.CurrentWorkspace.RootFile.Contents)
				require.Contains(t, res.CurrentWorkspace.RootDirectory.Entries, "root.txt")
				require.Contains(t, res.CurrentWorkspace.RootDirectory.Entries, "subdir/")
				require.NotContains(t, res.CurrentWorkspace.RootDirectory.Entries, "cwd.txt")
			})

			t.Run("workspace cwd", func(ctx context.Context, t *testctx.T) {
				cwdFilePath := "cwd.txt"
				wantCwdContents := "from workspace cwd"
				if tc.wantCwdPath == "" {
					cwdFilePath = "root.txt"
					wantCwdContents = "from workspace root"
				}

				res, err := testutil.Query[currentWorkspaceCwdResult](t, `query WorkspaceCwd($cwdFilePath: String!) {
					currentWorkspace {
						path
						cwd {
							path
							cwdFile: file(path: $cwdFilePath) {
								contents
							}
							cwdDirectory: directory(path: ".") {
								entries
							}
						}
					}
				}`, &testutil.QueryOptions{
					Variables: map[string]any{
						"cwdFilePath": cwdFilePath,
					},
				}, tc.clientOpts...)
				require.NoError(t, err)

				require.Equal(t, ".", res.CurrentWorkspace.Path)
				require.Equal(t, tc.wantCwdPath, res.CurrentWorkspace.Cwd.Path)
				require.Equal(t, wantCwdContents, res.CurrentWorkspace.Cwd.CwdFile.Contents)
				if tc.wantCwdPath == "" {
					require.Contains(t, res.CurrentWorkspace.Cwd.CwdDirectory.Entries, "root.txt")
					require.Contains(t, res.CurrentWorkspace.Cwd.CwdDirectory.Entries, "subdir/")
				} else {
					require.Contains(t, res.CurrentWorkspace.Cwd.CwdDirectory.Entries, "cwd.txt")
					require.NotContains(t, res.CurrentWorkspace.Cwd.CwdDirectory.Entries, "root.txt")
				}
			})
		})
	}
}

type workspaceCwdFixture struct {
	root string
	cwd  string
}

func newWorkspaceCwdFixture(t *testctx.T) workspaceCwdFixture {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".dagger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".dagger", "config.toml"), []byte("# workspace\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("from workspace root"), 0o600))

	cwd := filepath.Join(root, "subdir")
	require.NoError(t, os.MkdirAll(cwd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "cwd.txt"), []byte("from workspace cwd"), 0o600))

	return workspaceCwdFixture{
		root: root,
		cwd:  cwd,
	}
}

type currentWorkspaceRootResult struct {
	CurrentWorkspace struct {
		Path          string
		RootFile      fileContentsResult
		RootDirectory directoryEntriesResult
	}
}

type currentWorkspaceCwdResult struct {
	CurrentWorkspace struct {
		Path string
		Cwd  struct {
			Path         string
			CwdFile      fileContentsResult
			CwdDirectory directoryEntriesResult
		}
	}
}

type fileContentsResult struct {
	Contents string
}

type directoryEntriesResult struct {
	Entries []string
}
