package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// A nested client's served schema, host inertness and caller agent are
// engine-internal metadata: they never cross the wire, so they must survive
// the JSON round trip that snapshots a client's metadata.
func TestClientMetadataKeepsEngineInternalFields(t *testing.T) {
	t.Parallel()
	deps := &core.SchemaBuilder{}
	agent := dagql.ObjectResult[*core.Agent]{}
	cloned, err := cloneClientMetadata(&engine.ClientMetadata{ClientID: "nested", ServedDeps: deps, InertAttachables: true, CallerAgent: agent})
	require.NoError(t, err)
	require.Same(t, deps, nestedServedDeps(cloned))
	require.True(t, cloned.InertAttachables)
	require.Equal(t, any(agent), cloned.CallerAgent)
	require.Nil(t, nestedServedDeps(&engine.ClientMetadata{}))
	require.Nil(t, nestedServedDeps(nil))
}

// Requests run with the metadata of their client scope, not the record's:
// a host-inert client's requests must still say it is inert, or host lookups
// (an outer .env for a git module source) ask a host the server denies.
func TestClientScopeKeepsInertAttachables(t *testing.T) {
	t.Parallel()
	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test", func() {}, nil)
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "session", ClientID: "inert", InertAttachables: true}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	require.True(t, md.InertAttachables)
}

// A client nested under an inert one inherits the server's denial without
// declaring it; sealing its metadata records it.
func TestSealedMetadataRecordsInheritedInertAttachables(t *testing.T) {
	t.Parallel()
	sess := &daggerSession{}
	record := &clientRecord{
		clientID:         "child",
		clientMetadata:   &engine.ClientMetadata{SessionID: "session", ClientID: "child"},
		inertAttachables: true,
		accepting:        true,
	}
	require.NoError(t, sess.sealClientMetadataLocked(record))
	require.True(t, record.clientMetadata.InertAttachables)
}
