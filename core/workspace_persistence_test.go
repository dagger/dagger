package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func TestWorkspacePersistsModuleClients(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	const modulePath = ".dagger/modules/caller"
	clients := map[string][]string{modulePath: {".dagger/modules/hello"}}
	ws := &Workspace{Address: "local:///host/ws", Cwd: ".", ConfigFile: "dagger.toml"}
	ws.SetHostPath("/host/ws")
	ws.SetSelectedEnv("dev")
	ws.SetModuleClients(clients)
	require.Equal(t, clients[modulePath], ws.ModuleClients(modulePath))
	encoded, err := ws.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
	require.NoError(t, err)
	decoded, err := (&Workspace{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), encoded.JSON)
	require.NoError(t, err)
	restored := decoded.(*Workspace)
	require.Equal(t, "dev", restored.SelectedEnv())
	require.Equal(t, clients[modulePath], restored.ModuleClients(modulePath))
}

// TestWorkspacePersistsUserConfig checks that a restored workspace keeps the
// user-level config it was loaded with: it changes the workspace's effective
// config, and the workspace's content identity covers it.
func TestWorkspacePersistsUserConfig(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	overlay := &workspacepkg.UserWorkspaceOverlay{
		Modules: map[string]workspacepkg.EnvModuleOverlay{
			"greeter": {Settings: map[string]any{"greeting": "hola", "retries": int64(9007199254740993)}},
		},
		Env: map[string]workspacepkg.EnvOverlay{},
	}
	ws := &Workspace{Address: "git://example.com/repo", Cwd: ".", ConfigFile: "dagger.toml"}
	ws.SetUserConfigKey("example.com/repo")
	ws.SetUserConfigOverlay(overlay)
	encoded, err := ws.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
	require.NoError(t, err)
	decoded, err := (&Workspace{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), encoded.JSON)
	require.NoError(t, err)
	restored := decoded.(*Workspace)
	require.Equal(t, "example.com/repo", restored.UserConfigKey())
	require.Equal(t, overlay, restored.UserConfigOverlay())
}
