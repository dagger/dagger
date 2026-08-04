package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
)

const testUserConfigPath = "/home/alice/.config/dagger/config.toml"

// userConfigTestHost fakes a local host filesystem: a git repo at /repo with a
// dagger.toml, plus an optional user-level config file outside the repo.
type userConfigTestHost struct {
	files map[string]string
	dirs  map[string]bool
}

func newUserConfigTestHost(gitConfig, daggerTOML, userConfig string) *userConfigTestHost {
	h := &userConfigTestHost{
		files: map[string]string{
			"/repo/dagger.toml": daggerTOML,
		},
		dirs: map[string]bool{
			"/repo/.git": true,
		},
	}
	if gitConfig != "" {
		h.files["/repo/.git/config"] = gitConfig
	}
	if userConfig != "" {
		h.files[testUserConfigPath] = userConfig
	}
	return h
}

func (h *userConfigTestHost) statFS() core.StatFS {
	return core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if _, ok := h.files[path]; ok || h.dirs[path] {
			return filepath.Dir(path), &core.Stat{Name: filepath.Base(path)}, nil
		}
		return "", nil, os.ErrNotExist
	})
}

func (h *userConfigTestHost) readFile(_ context.Context, path string) ([]byte, error) {
	if data, ok := h.files[filepath.Clean(path)]; ok {
		return []byte(data), nil
	}
	return nil, os.ErrNotExist
}

func loadUserConfigTestWorkspace(t *testing.T, h *userConfigTestHost, md *engine.ClientMetadata) (*daggerClient, error) {
	t.Helper()

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})
	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata:       md,
	}
	err := (&Server{}).detectAndLoadWorkspace(ctx, client,
		h.statFS(),
		h.readFile,
		"/repo",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true, // isLocal
	)
	return client, err
}

const userConfigTestDaggerTOML = `[modules.aws]
source = "github.com/dagger/aws@main"

[modules.aws.settings]
profile = "shared"
region = "us-east-1"

[env.staging.modules.aws.settings]
profile = "staging"
`

const userConfigTestGitConfig = `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:acme/api.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`

func pendingModuleByName(t *testing.T, client *daggerClient, name string) pendingModule {
	t.Helper()
	for _, mod := range client.pendingModules {
		if mod.Name == name {
			return mod
		}
	}
	t.Fatalf("module %q not found in pending modules", name)
	return pendingModule{}
}

func TestUserConfigOverridesWorkspaceSettings(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`)
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	require.Equal(t, "github.com/acme/api", client.workspace.UserConfigKey())
	require.NotNil(t, client.workspace.UserConfigOverlay())

	aws := pendingModuleByName(t, client, "aws")
	// User-level value wins; untouched keys keep repo values.
	require.Equal(t, "alice-dev", aws.ConfigDefaults["profile"])
	require.Equal(t, "us-east-1", aws.ConfigDefaults["region"])
}

func TestUserConfigAddsSelectableEnv(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."github.com/acme/api".env.dev.modules.aws.settings]
profile = "alice-dev"
region = "us-west-2"
`)
	env := "dev"
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
		WorkspaceEnv:         &env,
	})
	require.NoError(t, err)

	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "alice-dev", aws.ConfigDefaults["profile"])
	require.Equal(t, "us-west-2", aws.ConfigDefaults["region"])
}

func TestUserConfigEnvMergesOverRepoEnv(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."github.com/acme/api".env.staging.modules.aws.settings]
region = "eu-west-1"
`)
	env := "staging"
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
		WorkspaceEnv:         &env,
	})
	require.NoError(t, err)

	aws := pendingModuleByName(t, client, "aws")
	// Repo env value survives; user env value layers on top.
	require.Equal(t, "staging", aws.ConfigDefaults["profile"])
	require.Equal(t, "eu-west-1", aws.ConfigDefaults["region"])
}

func TestUserConfigMatchesEquivalentRemoteForms(t *testing.T) {
	t.Parallel()

	// Repo origin is scp-style ssh; the user config keys the same remote in
	// https form with a .git suffix.
	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."https://github.com/acme/api.git".modules.aws.settings]
profile = "alice-dev"
`)
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "alice-dev", aws.ConfigDefaults["profile"])
}

func TestUserConfigOtherWorkspaceDoesNotApply(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."github.com/acme/other".modules.aws.settings]
profile = "alice-dev"
`)
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	require.Nil(t, client.workspace.UserConfigOverlay())
	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "shared", aws.ConfigDefaults["profile"])
}

func TestUserConfigNoGitRemote(t *testing.T) {
	t.Parallel()

	// A git workspace with no origin remote has no user-config key, so
	// user-level overrides never match it.
	h := newUserConfigTestHost("[core]\n\tbare = false\n", userConfigTestDaggerTOML, `
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`)
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	require.Empty(t, client.workspace.UserConfigKey())
	require.Nil(t, client.workspace.UserConfigOverlay())
	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "shared", aws.ConfigDefaults["profile"])
}

func TestUserConfigLocalPathRemote(t *testing.T) {
	t.Parallel()

	// Filesystem remotes have no stable cross-machine identity and produce no
	// key.
	h := newUserConfigTestHost(`[remote "origin"]
	url = /home/alice/src/api
`, userConfigTestDaggerTOML, "")
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)
	require.Empty(t, client.workspace.UserConfigKey())
}

func TestUserConfigMissingFileIsFine(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, "")
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	// The key is still derived, but with no user config there is no overlay.
	require.Equal(t, "github.com/acme/api", client.workspace.UserConfigKey())
	require.Nil(t, client.workspace.UserConfigOverlay())
	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "shared", aws.ConfigDefaults["profile"])
}

func TestUserConfigMalformedErrors(t *testing.T) {
	t.Parallel()

	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, "[workspaces")
	_, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.ErrorContains(t, err, "parsing user config")
}

func TestUserConfigNoPathDeclared(t *testing.T) {
	t.Parallel()

	// Clients that do not declare a user config path (e.g. older CLIs) get no
	// user-level behavior at all.
	h := newUserConfigTestHost(userConfigTestGitConfig, userConfigTestDaggerTOML, `
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`)
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
	})
	require.NoError(t, err)

	require.Empty(t, client.workspace.UserConfigKey())
	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "shared", aws.ConfigDefaults["profile"])
}

func TestUserConfigWorktreeGitFile(t *testing.T) {
	t.Parallel()

	// A linked worktree stores a .git *file* pointing at the main repo's
	// worktree gitdir; the shared config lives in the common git dir.
	h := &userConfigTestHost{
		files: map[string]string{
			"/repo/dagger.toml":                      userConfigTestDaggerTOML,
			"/repo/.git":                             "gitdir: /main/.git/worktrees/feature\n",
			"/main/.git/worktrees/feature/commondir": "../..\n",
			"/main/.git/config":                      userConfigTestGitConfig,
			testUserConfigPath: `
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`,
		},
	}
	client, err := loadUserConfigTestWorkspace(t, h, &engine.ClientMetadata{
		LoadWorkspaceModules: true,
		UserConfigPath:       testUserConfigPath,
	})
	require.NoError(t, err)

	require.Equal(t, "github.com/acme/api", client.workspace.UserConfigKey())
	aws := pendingModuleByName(t, client, "aws")
	require.Equal(t, "alice-dev", aws.ConfigDefaults["profile"])
}

func TestUserConfigRemoteWorkspaceKey(t *testing.T) {
	t.Parallel()

	// attachUserWorkspaceOverlay with a pre-computed remote key: the tree is
	// remote but the user config still comes from the caller's host.
	ws := &workspace.Workspace{Root: ".", Cwd: ".", ConfigFile: workspace.ConfigFileName}
	coreWS := &core.Workspace{}
	hostReadFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == testUserConfigPath {
			return []byte(`
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`), nil
		}
		return nil, os.ErrNotExist
	}

	err := attachUserWorkspaceOverlay(context.Background(),
		&engine.ClientMetadata{UserConfigPath: testUserConfigPath},
		nil,
		hostReadFile,
		ws,
		coreWS,
		workspace.NormalizeGitRemote("https://github.com/acme/api.git"),
		false, // isLocal
	)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/api", coreWS.UserConfigKey())
	overlay := coreWS.UserConfigOverlay()
	require.NotNil(t, overlay)
	require.Equal(t, "alice-dev", overlay.Modules["aws"].Settings["profile"])
}
