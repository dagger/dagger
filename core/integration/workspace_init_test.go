package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceInit(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name      string
		noGit     bool
		cwd       string
		files     map[string]string
		config    string
		wantError string
	}{
		{name: "new workspace", config: "dagger.toml"},
		{name: "new workspace from subdirectory", cwd: "child", config: "dagger.toml"},
		{name: "no Git repository", noGit: true, wantError: "workspace export requires a local Git workspace"},
		{
			name: "native config without Git is unchanged", noGit: true,
			files:     map[string]string{"dagger.toml": "# keep this\n"},
			wantError: "workspace export requires a local Git workspace",
		},
		{
			name: "existing native workspace", config: "dagger.toml",
			files: map[string]string{"dagger.toml": "# keep this\n", "dagger.json": `{"toolchains":[{"source":"./missing"}]}`},
		},
		{
			name: "nearest native config", cwd: "child", config: "child/dagger.toml",
			files: map[string]string{"dagger.toml": "# root\n", "child/dagger.toml": "# child\n"},
		},
		{
			name: "plain module", config: "dagger.toml",
			files: map[string]string{"dagger.json": `{"name":"app","sdk":"go"}`},
		},
		{
			name: "normalized source", config: "dagger.toml",
			files: map[string]string{"dagger.json": `{"name":"app","sdk":"go","source":"src/..","toolchains":[]}`},
		},
		{
			name: "workspace source", wantError: "run dagger workspace migrate instead",
			files: map[string]string{"dagger.json": `{"name":"app","sdk":"go","source":"src"}`},
		},
		{
			name: "no SDK", wantError: "run dagger workspace migrate instead",
			files: map[string]string{"dagger.json": `{}`},
		},
		{
			name: "toolchains", wantError: "run dagger workspace migrate instead",
			files: map[string]string{"dagger.json": `{"sdk":"go","toolchains":[{"source":"./tool"}]}`},
		},
		{
			name: "blueprint", wantError: "run dagger workspace migrate instead",
			files: map[string]string{"dagger.json": `{"blueprint":{"source":"./template"}}`},
		},
		{
			name: "plain module below legacy workspace", cwd: "child",
			wantError: "run dagger workspace migrate instead",
			files: map[string]string{
				"dagger.json":       `{"toolchains":[{"source":"./tool"}]}`,
				"child/dagger.json": `{"name":"app","sdk":"go"}`,
			},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			if !tc.noGit {
				initGitRepo(ctx, t, root)
			}
			for name, data := range tc.files {
				filename := filepath.Join(root, filepath.FromSlash(name))
				require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
				require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
			}
			cwd := filepath.Join(root, tc.cwd)
			require.NoError(t, os.MkdirAll(cwd, 0o755))
			out, err := hostDaggerExec(ctx, t, cwd, "--silent", "init", "--auto-apply")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.NotContains(t, string(out), "Workspace configuration initialized")
				require.NotContains(t, string(out), "module recommend")
				require.NotContains(t, string(out), "cloud checks on")
				if _, exists := tc.files["dagger.toml"]; !exists {
					require.NoFileExists(t, filepath.Join(root, "dagger.toml"))
				}
			} else {
				require.NoError(t, err)
				verb := "initialized"
				if _, exists := tc.files[tc.config]; exists {
					verb = "found"
				} else {
					data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.config)))
					require.NoError(t, err)
					require.Empty(t, data)
				}
				require.Contains(t, string(out), "Workspace configuration "+verb+" at ")
				require.Contains(t, string(out), "#!/bin/sh\n")
				require.Contains(t, string(out), "dagger module recommend\n")
				require.Contains(t, string(out), "dagger cloud checks on\n")
				if tc.config != "child/dagger.toml" {
					require.NoFileExists(t, filepath.Join(root, "child", "dagger.toml"))
				}
			}
			for name, original := range tc.files {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
				require.NoError(t, err)
				require.Equal(t, original, string(data), "%s must be unchanged", name)
			}
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithInitialized(ctx context.Context, t *testctx.T) {
	t.Run("removed selected config is initialized again", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		initGitRepo(ctx, t, root)
		writeWorkspaceConfigFile(t, root, "# old config\n")
		c := connect(ctx, t, dagger.WithWorkdir(root))
		initialized := c.CurrentWorkspace().WithoutFile("dagger.toml").WithInitialized()
		data, err := initialized.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Empty(t, data)
		dataBytes, err := os.ReadFile(filepath.Join(root, "dagger.toml"))
		require.NoError(t, err)
		require.Equal(t, "# old config\n", string(dataBytes), "host config stays unchanged until export")
		require.NoError(t, initialized.Export(ctx))
		dataBytes, err = os.ReadFile(filepath.Join(root, "dagger.toml"))
		require.NoError(t, err)
		require.Empty(t, dataBytes)
	})
	t.Run("selected config survives cwd change", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		initGitRepo(ctx, t, root)
		writeWorkspaceConfigFile(t, filepath.Join(root, "selected"), "# selected config\n")
		writeWorkspaceConfigFile(t, filepath.Join(root, "other"), "# other config\n")
		c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(root, "selected")))
		moved := c.CurrentWorkspace().WithWorkdir("other")
		initialized := moved.WithInitialized()
		configPath, err := initialized.ConfigFile(ctx)
		require.NoError(t, err)
		require.Equal(t, "../selected/dagger.toml", configPath)
		data, err := initialized.File(configPath).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "# selected config\n", data)
		unchanged, err := initialized.Changes(dagger.WorkspaceChangesOpts{From: moved}).IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, unchanged)
	})
	for _, configPath := range []string{"dagger.toml", "child/dagger.toml"} {
		t.Run("preserve staged "+configPath, func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			initGitRepo(ctx, t, root)
			c := connect(ctx, t, dagger.WithWorkdir(root))
			const contents = "# preserve these bytes\n"
			staged := c.CurrentWorkspace().
				WithNewFile(configPath, contents, dagger.WorkspaceWithNewFileOpts{Permissions: 0o600}).
				WithWorkdir(filepath.ToSlash(filepath.Dir(configPath)))
			initialized := staged.WithInitialized()
			selected, err := initialized.ConfigFile(ctx)
			require.NoError(t, err)
			require.Equal(t, "dagger.toml", selected)
			data, err := initialized.File("dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, contents, data)
			unchanged, err := initialized.Changes(dagger.WorkspaceChangesOpts{From: staged}).IsEmpty(ctx)
			require.NoError(t, err)
			require.True(t, unchanged, "initialization must not rewrite a staged config")
			require.NoError(t, initialized.Export(ctx))
			dataBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(configPath)))
			require.NoError(t, err)
			require.Equal(t, contents, string(dataBytes))
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(configPath)))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			if configPath != "dagger.toml" {
				require.NoFileExists(t, filepath.Join(root, "dagger.toml"))
			}
		})
	}
	t.Run("mounted config is read-only", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		initGitRepo(ctx, t, root)
		c := connect(ctx, t, dagger.WithWorkdir(root))
		mounted := c.Directory().WithNewFile("dagger.toml", "# mounted\n").File("dagger.toml")
		_, err := c.CurrentWorkspace().WithMountedFile("dagger.toml", mounted).WithInitialized().ID(ctx)
		require.ErrorContains(t, err, "is a read-only mount and cannot be modified")
		require.NoFileExists(t, filepath.Join(root, "dagger.toml"))
	})

	root := t.TempDir()
	initGitRepo(ctx, t, root)
	queryPath := writeQueryDoc(t, root, "init.graphql", `{
  currentWorkspace {
    withInitialized {
      configFile
      changes { addedPaths }
      withInitialized { configFile }
    }
  }
}`)
	out, err := hostDaggerOutput(ctx, t, root, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	var got struct {
		CurrentWorkspace struct {
			WithInitialized struct {
				ConfigFile      string
				Changes         struct{ AddedPaths []string }
				WithInitialized struct{ ConfigFile string }
			}
		}
	}
	require.NoError(t, json.Unmarshal(out, &got))
	initialized := got.CurrentWorkspace.WithInitialized
	require.Equal(t, "dagger.toml", initialized.ConfigFile)
	require.Equal(t, []string{"dagger.toml"}, initialized.Changes.AddedPaths)
	require.Equal(t, initialized.ConfigFile, initialized.WithInitialized.ConfigFile)
	require.NoFileExists(t, filepath.Join(root, "dagger.toml"), "initialization must stage changes until export")

	queryPath = writeQueryDoc(t, root, "export.graphql", `{
  currentWorkspace { withInitialized { export } }
}`)
	_, err = hostDaggerExec(ctx, t, root, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "dagger.toml"))
	out, err = hostDaggerExec(ctx, t, root, "--silent", "init", "--auto-apply")
	require.NoError(t, err)
	require.Contains(t, string(out), "Workspace configuration found at ./dagger.toml")
}
