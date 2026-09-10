package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientScopeIsImmutableCloneableAndRaceSafe(t *testing.T) {
	t.Parallel()

	var releases atomic.Int32
	var newLease func(ClientLeaseKind, string) *ClientLifecycleLease
	newLease = func(kind ClientLeaseKind, ownerID string) *ClientLifecycleLease {
		return NewClientLifecycleLease(
			kind,
			ownerID,
			func() { releases.Add(1) },
			func(kind ClientLeaseKind, ownerID string) (*ClientLifecycleLease, error) {
				return newLease(kind, ownerID), nil
			},
		)
	}

	metadata := &ClientMetadata{
		SessionID: "session",
		ClientID:  "client",
		Labels:    map[string]string{"immutable": "yes"},
	}
	scope, err := NewClientScope(metadata, newLease(ClientLeaseRequest, "request"))
	require.NoError(t, err)
	metadata.Labels["immutable"] = "mutated"

	got, err := scope.Metadata()
	require.NoError(t, err)
	require.Equal(t, "yes", got.Labels["immutable"])
	got.Labels["immutable"] = "also-mutated"
	gotAgain, err := scope.Metadata()
	require.NoError(t, err)
	require.Equal(t, "yes", gotAgain.Labels["immutable"])

	ctx, err := ContextWithClientScope(context.Background(), scope)
	require.NoError(t, err)
	detached, backgroundLease, err := DetachClientScope(ctx, ClientLeaseAgent, "agent")
	require.NoError(t, err)
	require.NotNil(t, backgroundLease)
	detachedScope, ok := ClientScopeFromContext(detached)
	require.True(t, ok)
	require.Equal(t, ClientLeaseAgent, detachedScope.Lease().Kind())
	require.Equal(t, "agent", detachedScope.Lease().OwnerID())

	var wg sync.WaitGroup
	for range 64 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			scope.Lease().Release()
		}()
		go func() {
			defer wg.Done()
			backgroundLease.Release()
		}()
	}
	wg.Wait()
	require.Equal(t, int32(2), releases.Load())
	require.False(t, scope.Lease().Held())
	require.False(t, backgroundLease.Held())
	_, err = scope.Clone(ClientLeaseSharedWork, "late")
	require.ErrorContains(t, err, "not held")
}

func TestDetachClientScopePreservesEffectiveMetadata(t *testing.T) {
	t.Parallel()

	var backgroundReleased atomic.Bool
	rootLease := NewClientLifecycleLease(
		ClientLeaseRequest,
		"request",
		func() {},
		func(kind ClientLeaseKind, ownerID string) (*ClientLifecycleLease, error) {
			return NewClientLifecycleLease(
				kind,
				ownerID,
				func() { backgroundReleased.Store(true) },
				nil,
			), nil
		},
	)
	t.Cleanup(rootLease.Release)

	scope, err := NewClientScope(&ClientMetadata{
		SessionID: "session",
		ClientID:  "execution-client",
	}, rootLease)
	require.NoError(t, err)
	ctx, err := ContextWithClientScope(context.Background(), scope)
	require.NoError(t, err)

	// Workspace host access executes under the caller's scope but routes through
	// the Workspace owner's metadata. Detaching that work must preserve both.
	routingMetadata := &ClientMetadata{
		SessionID: "session",
		ClientID:  "workspace-owner",
	}
	ctx = ContextWithClientMetadata(ctx, routingMetadata)
	detached, backgroundLease, err := DetachClientScope(ctx, ClientLeaseSharedWork, "call")
	require.NoError(t, err)
	require.NotNil(t, backgroundLease)
	t.Cleanup(backgroundLease.Release)

	detachedScope, ok := ClientScopeFromContext(detached)
	require.True(t, ok)
	require.Equal(t, "execution-client", detachedScope.ClientID())
	require.Equal(t, ClientLeaseSharedWork, detachedScope.Lease().Kind())
	require.Equal(t, "call", detachedScope.Lease().OwnerID())

	detachedMetadata, err := ClientMetadataFromContext(detached)
	require.NoError(t, err)
	require.Equal(t, routingMetadata, detachedMetadata)
	require.Equal(t, "workspace-owner", detachedMetadata.ClientID)

	backgroundLease.Release()
	require.True(t, backgroundReleased.Load())
}

func TestClientMetadataAppendToHTTPHeadersNormalizesLegacyWorkspaceModuleLoading(t *testing.T) {
	t.Parallel()

	headers := (&ClientMetadata{
		ClientID:             "client",
		ClientVersion:        "v1.2.3",
		ClientSecretToken:    "secret",
		LoadWorkspaceModules: false,
		SkipWorkspaceModules: true,
	}).AppendToHTTPHeaders(http.Header{})

	md, err := ClientMetadataFromHTTPHeaders(headers)
	require.NoError(t, err)
	require.False(t, md.LoadWorkspaceModules)
	require.False(t, md.SkipWorkspaceModules)
}

func TestClientMetadataFromHTTPHeadersRejectsConflictingWorkspaceModuleLoading(t *testing.T) {
	t.Parallel()

	headers := http.Header{}
	encoded, err := json.Marshal(ClientMetadata{
		ClientID:             "client",
		ClientVersion:        "v1.2.3",
		ClientSecretToken:    "secret",
		LoadWorkspaceModules: true,
		SkipWorkspaceModules: true,
	})
	require.NoError(t, err)
	headers.Set(ClientMetadataMetaKey, base64.StdEncoding.EncodeToString(encoded))

	_, err = ClientMetadataFromHTTPHeaders(headers)
	require.ErrorContains(t, err, "mutually exclusive")
}
