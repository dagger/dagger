package server

import (
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceReadEpochFollowsOwner(t *testing.T) {
	srv, sess, parent, parentCtx, parentScope := newNestedTransportTestFixture(t)
	defer parentScope.Lease().Release()
	defer sess.closeClientScope(parent)
	transport, err := srv.RegisterNestedClientTransport(parentCtx, nestedTransportTestMetadata("child"), parent.clientID)
	require.NoError(t, err)
	defer transport.Close()
	child := sess.clientRuntimes["child"]
	require.NoError(t, sealClientMetadataForTest(sess, child))
	scope, err := sess.acquireRootClientScope(child, engine.ClientLeaseRequest, "workspace read")
	require.NoError(t, err)
	defer scope.Lease().Release()
	childCtx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ownerCtx := engine.ContextWithClientMetadata(childCtx, parent.clientMetadata)

	epoch, err := srv.currentWorkspaceReadEpoch(ownerCtx)
	require.NoError(t, err)
	require.Empty(t, epoch)
	require.NoError(t, srv.bumpClientWorkspaceReadEpoch(parentCtx))
	epoch, err = srv.currentWorkspaceReadEpoch(ownerCtx)
	require.NoError(t, err)
	require.Equal(t, "1", epoch, "nested host reads must use the owner's invalidation counter")
	require.NoError(t, srv.bumpClientWorkspaceReadEpoch(ownerCtx))
	epoch, err = srv.currentWorkspaceReadEpoch(parentCtx)
	require.NoError(t, err)
	require.Equal(t, "2", epoch, "a nested reload must invalidate the owner's reads")
	epoch, err = srv.currentWorkspaceReadEpoch(childCtx)
	require.NoError(t, err)
	require.Empty(t, epoch, "owner invalidation must not change the nested client's cache")

	for _, md := range []*engine.ClientMetadata{
		{SessionID: "session", ClientID: "unrelated"},
		{SessionID: "other", ClientID: "parent"},
	} {
		ctx := engine.ContextWithClientMetadata(childCtx, md)
		_, err := srv.currentWorkspaceReadEpoch(ctx)
		require.Error(t, err)
		require.Error(t, srv.bumpClientWorkspaceReadEpoch(ctx))
	}
	scope.Lease().Release()
	_, err = srv.currentWorkspaceReadEpoch(ownerCtx)
	require.Error(t, err)
	require.Error(t, srv.bumpClientWorkspaceReadEpoch(ownerCtx))
}
