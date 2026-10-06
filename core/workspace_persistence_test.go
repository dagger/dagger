package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
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
