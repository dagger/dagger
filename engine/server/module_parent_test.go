package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
)

func TestModuleParentAndItsHost(t *testing.T) {
	t.Parallel()

	sess := &daggerSession{sessionID: "session"}
	sess.state.Store(sessionStateInitialized)
	newClient := func(id string, parents ...string) *clientRuntime {
		return &clientRuntime{clientRecord: &clientRecord{
			clientID: id, daggerSession: sess,
			clientMetadata:  &engine.ClientMetadata{ClientID: id, SessionID: "session"},
			parentClientIDs: parents, metadataSealed: true, accepting: true,
		}, state: clientStateInitialized, lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	}
	cli := newClient("cli")
	outer := newClient("outer", "cli")
	outer.mod = sessionTestModuleResult(t, "outer")
	outerExec := newClient("outer-exec", "cli", "outer")
	inner := newClient("inner", "cli", "outer", "outer-exec")
	inner.mod = sessionTestModuleResult(t, "inner")
	innerExec := newClient("inner-exec", "cli", "outer", "outer-exec", "inner")
	plainExec := newClient("plain-exec", "cli")
	sess.clientRuntimes = map[string]*clientRuntime{}
	for _, client := range []*clientRuntime{cli, outer, outerExec, inner, innerExec, plainExec} {
		sess.clientRuntimes[client.clientID] = client
	}
	installTestClientRecords(sess)
	srv := &Server{daggerSessions: map[string]*daggerSession{"session": sess}}
	clientContext := func(client *clientRuntime) context.Context {
		scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "module parent test")
		require.NoError(t, err)
		t.Cleanup(scope.Lease().Release)
		ctx, err := engine.ContextWithClientScope(t.Context(), scope)
		require.NoError(t, err)
		return ctx
	}

	for client, want := range map[*clientRuntime]string{
		outer:     "outer",
		outerExec: "outer",
		inner:     "inner",
		innerExec: "inner",
	} {
		mod, err := srv.ModuleParent(clientContext(client))
		require.NoError(t, err, client.clientID)
		require.Equal(t, want, mod.Self().Name(), client.clientID)
	}

	for _, client := range []*clientRuntime{cli, plainExec} {
		_, err := srv.ModuleParent(clientContext(client))
		require.ErrorIs(t, err, core.ErrNoCurrentModule, client.clientID)
	}

	_, err := srv.ModuleParent(engine.ContextWithClientMetadata(clientContext(plainExec), outer.clientMetadata))
	require.ErrorIs(t, err, core.ErrNoCurrentModule,
		"the caller is the client holding the scope, not the one its metadata names")

	for client, want := range map[*clientRuntime]string{
		outer:     "cli",
		outerExec: "cli",
		inner:     "outer-exec",
		innerExec: "outer-exec",
	} {
		md, err := srv.ModuleParentHostClientMetadata(clientContext(client))
		require.NoError(t, err, client.clientID)
		require.Equal(t, want, md.ClientID, client.clientID)
	}

	for _, client := range []*clientRuntime{cli, plainExec} {
		_, err := srv.ModuleParentHostClientMetadata(clientContext(client))
		require.ErrorIs(t, err, core.ErrNoCurrentModule, client.clientID)
	}
}
