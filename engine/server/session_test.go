package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func installTestClientRecords(sess *daggerSession) {
	if sess.clientRecords == nil {
		sess.clientRecords = make(map[string]*clientRecord, len(sess.clientRuntimes))
	}
	for id, runtime := range sess.clientRuntimes {
		if runtime == nil || runtime.clientRecord == nil {
			continue
		}
		runtime.daggerSession = sess
		sess.clientRecords[id] = runtime.clientRecord
	}
}

func newNestedTransportTestFixture(t *testing.T) (*Server, *daggerSession, *clientRuntime, context.Context, engine.ClientScope) {
	t.Helper()
	parent := &clientRuntime{clientRecord: &clientRecord{
		clientID:       "parent",
		clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "parent", ClientSecretToken: "parent-token"},
		metadataSealed: true,
		accepting:      true},
		state:           clientStateInitialized,
		lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	sess := &daggerSession{
		sessionID:          "session",
		mainClientCallerID: parent.clientID,
		clientRuntimes:     map[string]*clientRuntime{parent.clientID: parent},
	}
	parent.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	transport, err := sess.acquireRootClientScope(parent, engine.ClientLeaseTransport, "parent")
	require.NoError(t, err)
	parent.transportLease = transport.Lease()
	requestScope, err := sess.acquireRootClientScope(parent, engine.ClientLeaseRequest, "POST /query")
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(context.Background(), requestScope)
	require.NoError(t, err)
	return &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}, sess, parent, ctx, requestScope
}

func nestedTransportTestMetadata(clientID string) *engine.ClientMetadata {
	return &engine.ClientMetadata{
		SessionID:         "session",
		ClientID:          clientID,
		ClientSecretToken: clientID + "-token",
		ClientVersion:     engine.Version,
	}
}

func mergeClientMetadataForTest(sess *daggerSession, client *clientRuntime, metadata *engine.ClientMetadata) error {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	return sess.mergeClientMetadataLocked(client.clientRecord, metadata)
}

func sealClientMetadataForTest(sess *daggerSession, client *clientRuntime) error {
	sess.scopeMu.Lock()
	defer sess.scopeMu.Unlock()
	return sess.sealClientMetadataLocked(client.clientRecord)
}

func TestNestedTransportRegistrationIsStrictUniqueAndPermanent(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	metadata := nestedTransportTestMetadata("child")

	transport, err := srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.NoError(t, err)
	sess.clientMu.RLock()
	child := sess.clientRuntimes[metadata.ClientID]
	sess.clientMu.RUnlock()
	require.NotNil(t, child)
	require.Equal(t, clientStateUninitialized, child.state)
	require.Equal(t, []string{parent.clientID}, child.parentClientIDs)
	require.Same(t, transport, child.nestedTransport)

	_, err = srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.ErrorContains(t, err, "already registered")

	transport.Close()
	transport.Close()
	require.True(t, transport.Closed())
	accepting, _, childLeases := sess.clientLifecycleSnapshot(child)
	require.False(t, accepting)
	require.Empty(t, childLeases, "double close must release transport ownership exactly once")
	require.Equal(t, clientStateReclaimed, child.state)
	sess.clientMu.RLock()
	_, runtimeRetained := sess.clientRuntimes[metadata.ClientID]
	_, recordRetained := sess.clientRecords[metadata.ClientID]
	sess.clientMu.RUnlock()
	require.False(t, runtimeRetained, "idle close must reclaim the child runtime")
	require.True(t, recordRetained, "quiescence must keep the session-long identity record")
	debug := srv.ClientLifecycleDebugSnapshot()
	require.Equal(t, 2, debug.Records)
	require.Equal(t, 1, debug.Runtimes)
	require.Equal(t, "quiescent", debug.Sessions[0].Clients[0].RuntimeState)
	require.NotNil(t, debug.Sessions[0].Clients[0].ClosedAt)
	require.NotNil(t, debug.Sessions[0].Clients[0].QuiescentAt)

	_, err = srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.ErrorContains(t, err, "permanently closed")

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, engine.QueryEndpoint, strings.NewReader(`{"query":"{version}"}`))
	srv.ServeHTTPToNestedClient(recorder, req, transport, metadata, parent.clientID, false, nil, nil)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, clientStateReclaimed, child.state, "close before first request must leave a permanent record tombstone")

	// The child's serialized quiescent transition releases the exact child edge
	// it cloned from the parent.
	_, _, parentLeases := sess.clientLifecycleSnapshot(parent)
	for _, lease := range parentLeases {
		require.NotEqual(t, engine.ClientLeaseChild, lease.kind)
	}
}

func TestNestedTransportRegistrationRequiresExactHeldParentScope(t *testing.T) {
	t.Parallel()

	srv, _, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	metadata := nestedTransportTestMetadata("child")

	_, err := srv.RegisterNestedClientTransport(context.Background(), metadata, parent.clientID)
	require.ErrorContains(t, err, "requires a held parent client scope")

	_, err = srv.RegisterNestedClientTransport(ctx, metadata, "other")
	require.ErrorContains(t, err, "does not match")

	otherSessionMetadata := *metadata
	otherSessionMetadata.SessionID = "other-session"
	_, err = srv.RegisterNestedClientTransport(ctx, &otherSessionMetadata, parent.clientID)
	require.ErrorContains(t, err, "does not match")

	fakeLease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "fake", func() {}, func(kind engine.ClientLeaseKind, ownerID string) (*engine.ClientLifecycleLease, error) {
		return engine.NewClientLifecycleLease(kind, ownerID, func() {}, nil), nil
	})
	fakeScope, err := engine.NewClientScope(parent.clientMetadata, fakeLease)
	require.NoError(t, err)
	fakeCtx, err := engine.ContextWithClientScope(context.Background(), fakeScope)
	require.NoError(t, err)
	_, err = srv.RegisterNestedClientTransport(fakeCtx, metadata, parent.clientID)
	require.ErrorContains(t, err, "does not belong to the current session")

	requestScope.Lease().Release()
	_, err = srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.ErrorContains(t, err, "does not belong to the current session")

	// Equal string IDs from a replaced session record are still stale: the
	// session authority token must match by identity.
	_, _, _, staleCtx, staleScope := newNestedTransportTestFixture(t)
	defer staleScope.Lease().Release()
	freshSrv, _, freshParent, _, freshScope := newNestedTransportTestFixture(t)
	defer freshScope.Lease().Release()
	_, err = freshSrv.RegisterNestedClientTransport(staleCtx, metadata, freshParent.clientID)
	require.ErrorContains(t, err, "does not belong to the current session")
}

func TestHeldParentScopeDelegatesWhileParentClosing(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	sess.closeClientScope(parent)

	transport, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("child"), parent.clientID)
	require.NoError(t, err, "already accepted work must remain a delegation capability during parent close")
	transport.Close()
}

func TestClosedClientWaitsForAcceptedBootstrapRequest(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	metadata := nestedTransportTestMetadata("child")
	transport, err := srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
	require.NoError(t, err)
	child := sess.clientRuntimes[metadata.ClientID]

	_, cleanup, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  metadata,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.NoError(t, err)

	transport.Close()
	sess.clientMu.RLock()
	require.Same(t, child, sess.clientRuntimes[child.clientID],
		"an accepted bootstrap request must retain the closed runtime")
	sess.clientMu.RUnlock()
	_, _, leases := sess.clientLifecycleSnapshot(child)
	require.Equal(t, []clientLifecycleLeaseRecord{{kind: engine.ClientLeaseRequest, ownerID: "client connection"}}, leases)

	require.NoError(t, cleanup())
	require.NoError(t, cleanup(), "connection cleanup must be idempotent")
	sess.clientMu.RLock()
	_, runtimeRetained := sess.clientRuntimes[child.clientID]
	record := sess.clientRecords[child.clientID]
	sess.clientMu.RUnlock()
	require.False(t, runtimeRetained)
	require.NotNil(t, record)
	sess.scopeMu.Lock()
	require.False(t, record.quiescentAt.IsZero())
	sess.scopeMu.Unlock()
	child.stateMu.RLock()
	require.Zero(t, child.activeCount, "idempotent cleanup must decrement the request count once")
	child.stateMu.RUnlock()
}

func TestChildQuiescenceReleasesClosingParent(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	sess.closeClientScope(parent)
	childTransport, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("child"), parent.clientID)
	require.NoError(t, err, "the already accepted request may delegate while its parent closes")

	requestScope.Lease().Release()
	sess.clientMu.RLock()
	_, parentRetained := sess.clientRuntimes[parent.clientID]
	sess.clientMu.RUnlock()
	require.True(t, parentRetained, "the live child lease must retain its closing parent")
	_, _, parentLeases := sess.clientLifecycleSnapshot(parent)
	require.Equal(t, []clientLifecycleLeaseRecord{{kind: engine.ClientLeaseChild, ownerID: "child"}}, parentLeases)

	childTransport.Close()
	sess.clientMu.RLock()
	_, childRetained := sess.clientRuntimes["child"]
	_, parentRetained = sess.clientRuntimes[parent.clientID]
	_, childRecordRetained := sess.clientRecords["child"]
	_, parentRecordRetained := sess.clientRecords[parent.clientID]
	sess.clientMu.RUnlock()
	require.False(t, childRetained)
	require.False(t, parentRetained, "the child's quiescent transition must wake parent reclamation")
	require.True(t, childRecordRetained)
	require.True(t, parentRecordRetained)
}

func TestNestedShutdownClosesRegisteredTransportIdempotently(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	transport, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("child"), parent.clientID)
	require.NoError(t, err)
	child := sess.clientRuntimes["child"]
	sess.scopeMu.Lock()
	shutdownLease := sess.newClientLifecycleLeaseLocked(child, engine.ClientLeaseRequest, "POST /shutdown")
	sess.scopeMu.Unlock()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil)
	require.NoError(t, srv.serveShutdown(recorder, req, child))
	require.True(t, transport.Closed())
	transport.Close()
	accepting, _, leases := sess.clientLifecycleSnapshot(child)
	require.False(t, accepting)
	require.Equal(t, []clientLifecycleLeaseRecord{{kind: engine.ClientLeaseRequest, ownerID: "POST /shutdown"}}, leases)
	shutdownLease.Release()
	sess.clientMu.RLock()
	_, retained := sess.clientRuntimes[child.clientID]
	sess.clientMu.RUnlock()
	require.False(t, retained, "the accepted shutdown request must be the final owner")

	retry := httptest.NewRecorder()
	srv.ServeHTTPToNestedClient(
		retry,
		httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil),
		transport,
		nestedTransportTestMetadata("child"),
		parent.clientID,
		false,
		nil,
		nil,
	)
	require.Equal(t, http.StatusNoContent, retry.Code)
}

func TestNestedTransportCloseRacesInitializationSerialization(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	transport, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("child"), parent.clientID)
	require.NoError(t, err)
	child := sess.clientRuntimes["child"]

	started := make(chan struct{})
	release := make(chan struct{})
	initialized := make(chan struct{})
	go func() {
		sess.scopeMu.Lock()
		close(started)
		<-release
		if child.accepting {
			child.state = clientStateInitialized
		}
		sess.scopeMu.Unlock()
		close(initialized)
	}()
	<-started
	closed := make(chan struct{})
	go func() {
		transport.Close()
		close(closed)
	}()
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	for !transport.Closed() {
		select {
		case <-deadline.C:
			ticker.Stop()
			close(release)
			<-initialized
			<-closed
			t.Fatal("transport close did not publish its proxy-side marker")
		case <-ticker.C:
		}
	}
	deadline.Stop()
	ticker.Stop()
	select {
	case <-closed:
		close(release)
		<-initialized
		t.Fatal("transport close crossed in-progress initialization serialization")
	default:
	}
	close(release)
	<-initialized
	<-closed
	accepting, _, _ := sess.clientLifecycleSnapshot(child)
	require.False(t, accepting)

	// Run the opposite deterministic ordering: close wins before initialization.
	second, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("second"), parent.clientID)
	require.NoError(t, err)
	secondChild := sess.clientRuntimes["second"]
	second.Close()
	sess.scopeMu.Lock()
	if secondChild.accepting {
		secondChild.state = clientStateInitialized
	}
	sess.scopeMu.Unlock()
	require.Equal(t, clientStateReclaimed, secondChild.state)
}

func TestNestedBootstrapRequestsMergeBeforeSeal(t *testing.T) {
	t.Parallel()

	srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	registration := nestedTransportTestMetadata("child")
	transport, err := srv.RegisterNestedClientTransport(ctx, registration, parent.clientID)
	require.NoError(t, err)
	child := sess.clientRuntimes[registration.ClientID]

	attachables := *registration
	attachables.AllowedLLMModules = []string{"github.com/acme/mod"}
	_, cleanup, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  &attachables,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.NoError(t, err)
	require.NoError(t, cleanup())
	require.Equal(t, clientStateUninitialized, child.state)
	require.False(t, sess.clientMetadataSealed(child.clientRecord))

	init := attachables
	init.Workspace = stringPtr("github.com/acme/workspace@main")
	_, cleanup, err = srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  &init,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.NoError(t, err)
	require.NoError(t, cleanup())

	conflict := init
	conflict.Workspace = stringPtr("github.com/acme/other@main")
	_, _, err = srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  &conflict,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.ErrorContains(t, err, "Workspace")

	require.NoError(t, sealClientMetadataForTest(sess, child))
	sealed, err := sess.clientMetadataSnapshot(child.clientRecord)
	require.NoError(t, err)
	require.Equal(t, attachables.AllowedLLMModules, sealed.AllowedLLMModules)
	require.Equal(t, *init.Workspace, *sealed.Workspace)

	_, cleanup, err = srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  sealed,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.NoError(t, err, "identical bootstrap replay after seal must be accepted")
	require.NoError(t, cleanup())

	transport.Close()
	_, _, err = srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata:  sealed,
		NestedTransport: transport,
		ParentClientID:  parent.clientID,
		BootstrapOnly:   true,
	})
	require.ErrorContains(t, err, "closed")
}

func TestClientMetadataBootstrapOrderReplayAndConflict(t *testing.T) {
	t.Parallel()

	type contribution struct {
		name string
		md   *engine.ClientMetadata
	}
	identity := func() *engine.ClientMetadata {
		return &engine.ClientMetadata{
			SessionID:         "session",
			ClientID:          "client",
			ClientSecretToken: "token",
		}
	}
	registration := identity()
	registration.LockMode = "live"
	attachables := identity()
	attachables.AllowedLLMModules = []string{"github.com/acme/mod"}
	init := identity()
	init.Workspace = stringPtr("github.com/acme/workspace@main")
	query := identity()
	query.ExtraModules = []engine.ExtraModule{{Ref: "github.com/acme/extra@v1", Name: "extra", Entrypoint: true}}
	contributions := []contribution{{"attachables", attachables}, {"init", init}, {"first-request", query}}
	orders := [][]int{
		{0, 1, 2},
		{0, 2, 1},
		{1, 0, 2},
		{1, 2, 0},
		{2, 0, 1},
		{2, 1, 0},
	}

	for _, order := range orders {
		names := []string{contributions[order[0]].name, contributions[order[1]].name, contributions[order[2]].name}
		t.Run(strings.Join(names, "-"), func(t *testing.T) {
			client := &clientRuntime{clientRecord: &clientRecord{clientID: "client", accepting: true}}
			sess := &daggerSession{}
			require.NoError(t, mergeClientMetadataForTest(sess, client, registration))
			for _, index := range order {
				require.NoError(t, mergeClientMetadataForTest(sess, client, contributions[index].md))
			}
			require.NoError(t, sealClientMetadataForTest(sess, client))

			sealed, err := sess.clientMetadataSnapshot(client.clientRecord)
			require.NoError(t, err)
			require.Equal(t, "live", sealed.LockMode)
			require.Equal(t, attachables.AllowedLLMModules, sealed.AllowedLLMModules)
			require.Equal(t, *init.Workspace, *sealed.Workspace)
			require.Equal(t, query.ExtraModules, sealed.ExtraModules)

			// A complete identical replay is accepted after seal.
			require.NoError(t, mergeClientMetadataForTest(sess, client, sealed))

			completion := identity()
			completion.WorkspaceEnv = stringPtr("ci")
			require.ErrorContains(t, mergeClientMetadataForTest(sess, client, completion), "sealed")

			conflict := identity()
			conflict.Workspace = stringPtr("github.com/acme/other@main")
			require.ErrorContains(t, mergeClientMetadataForTest(sess, client, conflict), "Workspace")
		})
	}

	client := &clientRuntime{clientRecord: &clientRecord{clientID: "client", accepting: true}}
	sess := &daggerSession{}
	first := identity()
	first.ClientVersion = "v1.0.0"
	require.NoError(t, mergeClientMetadataForTest(sess, client, first))
	require.NoError(t, mergeClientMetadataForTest(sess, client, first), "identical replay before seal must be accepted")
	conflict := identity()
	conflict.ClientVersion = "v2.0.0"
	require.ErrorContains(t, mergeClientMetadataForTest(sess, client, conflict), "ClientVersion")
}

func TestClientMetadataBootstrapDeepCloneAndScopeSnapshot(t *testing.T) {
	t.Parallel()

	workspace := "github.com/acme/workspace@main"
	workspaceEnv := "ci"
	input := &engine.ClientMetadata{
		SessionID:          "session",
		ClientID:           "client",
		ClientSecretToken:  "token",
		Labels:             map[string]string{"branch": "main"},
		InteractiveCommand: []string{"sh", "-l"},
		AllowedLLMModules:  []string{"github.com/acme/mod"},
		UpstreamCacheImportConfig: []*controlapi.CacheOptionsEntry{{
			Type:  "registry",
			Attrs: map[string]string{"ref": "registry.example/acme/cache"},
		}},
		ExtraModules:          []engine.ExtraModule{{Ref: "github.com/acme/extra@v1", Name: "extra"}},
		Workspace:             &workspace,
		WorkspaceEnv:          &workspaceEnv,
		UseRecipeIDsByDefault: true,
	}
	client := &clientRuntime{clientRecord: &clientRecord{clientID: input.ClientID, accepting: true}}
	sess := &daggerSession{}
	require.NoError(t, mergeClientMetadataForTest(sess, client, input))

	input.Labels["branch"] = "mutated"
	input.InteractiveCommand[0] = "mutated"
	input.AllowedLLMModules[0] = "mutated"
	input.UpstreamCacheImportConfig[0].Attrs["ref"] = "mutated"
	input.ExtraModules[0].Ref = "mutated"
	*input.Workspace = "mutated"
	*input.WorkspaceEnv = "mutated"

	require.NoError(t, sealClientMetadataForTest(sess, client))
	lookup, err := sess.clientMetadataSnapshot(client.clientRecord)
	require.NoError(t, err)
	lookup.Labels["branch"] = "lookup-mutated"
	lookup.ExtraModules[0].Ref = "lookup-mutated"
	lookupAgain, err := sess.clientMetadataSnapshot(client.clientRecord)
	require.NoError(t, err)
	require.Equal(t, "main", lookupAgain.Labels["branch"])
	require.Equal(t, "github.com/acme/extra@v1", lookupAgain.ExtraModules[0].Ref)

	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test", func() {}, nil)
	scope, err := engine.NewClientScope(client.clientMetadata, lease)
	require.NoError(t, err)
	first, err := scope.Metadata()
	require.NoError(t, err)
	require.Equal(t, "main", first.Labels["branch"])
	require.Equal(t, "sh", first.InteractiveCommand[0])
	require.Equal(t, "github.com/acme/mod", first.AllowedLLMModules[0])
	require.Equal(t, "registry.example/acme/cache", first.UpstreamCacheImportConfig[0].Attrs["ref"])
	require.Equal(t, "github.com/acme/extra@v1", first.ExtraModules[0].Ref)
	require.Equal(t, "github.com/acme/workspace@main", *first.Workspace)
	require.Equal(t, "ci", *first.WorkspaceEnv)
	require.True(t, first.UseRecipeIDsByDefault)

	first.Labels["branch"] = "lookup-mutated"
	first.ExtraModules[0].Ref = "lookup-mutated"
	second, err := scope.Metadata()
	require.NoError(t, err)
	require.Equal(t, "main", second.Labels["branch"])
	require.Equal(t, "github.com/acme/extra@v1", second.ExtraModules[0].Ref)
	lease.Release()
}

func TestClientMetadataSealRacesTransportClose(t *testing.T) {
	t.Parallel()

	for range 100 {
		srv, sess, parent, ctx, requestScope := newNestedTransportTestFixture(t)
		metadata := nestedTransportTestMetadata("child")
		transport, err := srv.RegisterNestedClientTransport(ctx, metadata, parent.clientID)
		require.NoError(t, err)
		child := sess.clientRuntimes[metadata.ClientID]

		start := make(chan struct{})
		sealErr := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			sess.scopeMu.Lock()
			defer sess.scopeMu.Unlock()
			if !child.accepting || transport.Closed() {
				return
			}
			if err := sess.mergeClientMetadataLocked(child.clientRecord, metadata); err != nil {
				sealErr <- err
				return
			}
			if err := sess.sealClientMetadataLocked(child.clientRecord); err != nil {
				sealErr <- err
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			transport.Close()
		}()
		close(start)
		wg.Wait()
		select {
		case err := <-sealErr:
			require.ErrorContains(t, err, "closed")
		default:
		}

		accepting, _, leases := sess.clientLifecycleSnapshot(child)
		require.False(t, accepting)
		require.Empty(t, leases)
		if sess.clientMetadataSealed(child.clientRecord) {
			_, err := sess.clientMetadataSnapshot(child.clientRecord)
			require.NoError(t, err)
		}
		requestScope.Lease().Release()
	}
}

func TestSessionAttachablesLabelsAllowQueryAndShutdownAdmission(t *testing.T) {
	t.Parallel()

	metadata := &engine.ClientMetadata{
		SessionID:         "session",
		ClientID:          "client",
		ClientSecretToken: "token",
		Labels:            map[string]string{"dagger.io/sdk.name": "go"},
	}
	recordMetadata, err := cloneClientMetadata(metadata)
	require.NoError(t, err)

	analyticsLabels := sessionAnalyticsLabels(metadata, "test-engine").AsMap()
	require.Equal(t, "test-engine", analyticsLabels["dagger.io/engine"])
	require.Contains(t, analyticsLabels, "dagger.io/server.version")
	require.Equal(t, map[string]string{"dagger.io/sdk.name": "go"}, metadata.Labels,
		"session analytics labels must not mutate request metadata")

	record := &clientRecord{
		clientID:       metadata.ClientID,
		clientMetadata: recordMetadata,
		metadataSealed: true,
		accepting:      true,
	}
	runtime := &clientRuntime{
		clientRecord:    record,
		state:           clientStateInitialized,
		lifecycleLeases: map[uint64]clientLifecycleLeaseRecord{},
	}
	sess := &daggerSession{
		sessionID:          metadata.SessionID,
		mainClientCallerID: "main",
		clientRecords:      map[string]*clientRecord{metadata.ClientID: record},
		clientRuntimes:     map[string]*clientRuntime{metadata.ClientID: runtime},
	}
	record.daggerSession = sess
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{metadata.SessionID: sess}}

	for _, request := range []struct {
		name          string
		bootstrapOnly bool
	}{
		{name: "query", bootstrapOnly: false},
		{name: "shutdown", bootstrapOnly: true},
	} {
		t.Run(request.name, func(t *testing.T) {
			client, cleanup, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
				ClientMetadata: metadata,
				RootClient:     true,
				BootstrapOnly:  request.bootstrapOnly,
			})
			require.NoError(t, err)
			require.Same(t, runtime, client)
			require.NoError(t, cleanup())
		})
	}
}

func TestClientRecordLookupsAreIndependentFromExecutableRuntime(t *testing.T) {
	t.Parallel()

	root := &clientRecord{
		clientID:        "root",
		clientMetadata:  &engine.ClientMetadata{SessionID: "session", ClientID: "root", Labels: map[string]string{"kind": "record"}},
		metadataSealed:  true,
		accepting:       true,
		parentClientIDs: nil,
	}
	child := &clientRecord{
		clientID:        "child",
		clientMetadata:  &engine.ClientMetadata{SessionID: "session", ClientID: "child"},
		metadataSealed:  true,
		accepting:       true,
		parentClientIDs: []string{"root"},
	}
	sess := &daggerSession{
		sessionID: "session",
		clientRecords: map[string]*clientRecord{
			root.clientID:  root,
			child.clientID: child,
		},
		clientRuntimes: map[string]*clientRuntime{},
	}
	root.daggerSession = sess
	child.daggerSession = sess
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}

	ctx := engine.ContextWithClientMetadata(context.Background(), child.clientMetadata)
	metadata, err := srv.SpecificClientMetadata(ctx, root.clientID)
	require.NoError(t, err)
	require.Equal(t, "record", metadata.Labels["kind"])
	route, err := sess.telemetryRouteOriginClientID(child.clientID)
	require.NoError(t, err)
	require.Equal(t, []string{"child", "root"}, route)

	_, err = sess.clientRuntimeForRecord(child)
	require.ErrorContains(t, err, "not retained")
	_, err = srv.executableClientFromContext(ctx)
	require.ErrorContains(t, err, "requires a client scope")

	runtime := &clientRuntime{
		clientRecord:    child,
		state:           clientStateInitialized,
		lifecycleLeases: map[uint64]clientLifecycleLeaseRecord{},
	}
	sess.clientMu.Lock()
	sess.clientRuntimes[child.clientID] = runtime
	sess.clientMu.Unlock()

	_, err = srv.executableClientFromContext(ctx)
	require.ErrorContains(t, err, "requires a client scope",
		"metadata identity alone must not authorize executable runtime access")
	scope, err := sess.acquireRootClientScope(runtime, engine.ClientLeaseRequest, "test")
	require.NoError(t, err)
	scopeCtx, err := engine.ContextWithClientScope(context.Background(), scope)
	require.NoError(t, err)
	got, err := srv.executableClientFromContext(scopeCtx)
	require.NoError(t, err)
	require.Same(t, runtime, got)
	scope.Lease().Release()

	_, _, leases := sess.clientLifecycleSnapshot(runtime)
	require.Empty(t, leases)
	sess.clientMu.RLock()
	retained := sess.clientRuntimes[child.clientID]
	sess.clientMu.RUnlock()
	require.Same(t, runtime, retained, "an open transport keeps its runtime reachable even with no request leases")
	sess.closeClientScope(runtime)
	sess.clientMu.RLock()
	_, retainedAfterClose := sess.clientRuntimes[child.clientID]
	sess.clientMu.RUnlock()
	require.False(t, retainedAfterClose, "closing a zero-lease runtime must reclaim it")
	require.Contains(t, sess.clientRecords, child.clientID)
}

func TestWorkspaceHostAccessDoesNotRequireOwnerRuntime(t *testing.T) {
	t.Parallel()

	owner := &clientRecord{
		clientID: "owner",
		clientMetadata: &engine.ClientMetadata{
			SessionID: "session",
			ClientID:  "owner",
			Labels:    map[string]string{"source": "record"},
		},
		metadataSealed: true,
		accepting:      true,
	}
	callerRecord := &clientRecord{
		clientID: "caller",
		clientMetadata: &engine.ClientMetadata{
			SessionID: "session",
			ClientID:  "caller",
		},
		metadataSealed: true,
		accepting:      true,
	}
	callerRuntime := &clientRuntime{
		clientRecord:    callerRecord,
		state:           clientStateInitialized,
		lifecycleLeases: map[uint64]clientLifecycleLeaseRecord{},
	}
	ownerAttachable := &sessionAttachableCaller{
		ctx:       context.Background(),
		supported: map[string]struct{}{},
	}
	attachables := newSessionAttachableManager()
	attachables.callers[owner.clientID] = ownerAttachable
	sess := &daggerSession{
		sessionID:   "session",
		attachables: attachables,
		clientRecords: map[string]*clientRecord{
			owner.clientID:        owner,
			callerRecord.clientID: callerRecord,
		},
		clientRuntimes: map[string]*clientRuntime{
			callerRecord.clientID: callerRuntime,
		},
	}
	owner.daggerSession = sess
	callerRecord.daggerSession = sess
	sess.getClientCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		return sess.attachables.Wait(ctx, id)
	}
	sess.engineUtilClient = &engineutil.Client{Opts: &engineutil.Opts{
		GetClientCaller:      sess.getClientCaller,
		GetHostServiceCaller: sess.resolveHostServiceCaller,
	}}
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}

	scope, err := sess.acquireRootClientScope(callerRuntime, engine.ClientLeaseRequest, "workspace access")
	require.NoError(t, err)
	defer scope.Lease().Release()
	callerCtx, err := engine.ContextWithClientScope(context.Background(), scope)
	require.NoError(t, err)

	workspaceCtx, gateway, err := srv.workspaceOwnerAccess(callerCtx, sess, &core.Workspace{ClientID: owner.clientID})
	require.NoError(t, err)
	require.Same(t, sess.engineUtilClient, gateway)
	require.NotContains(t, sess.clientRuntimes, owner.clientID,
		"workspace owner access must not require a retained owner runtime")

	ownerMetadata, err := engine.ClientMetadataFromContext(workspaceCtx)
	require.NoError(t, err)
	require.Equal(t, owner.clientID, ownerMetadata.ClientID)
	ownerMetadata.Labels["source"] = "mutated"

	caller, err := gateway.GetSessionCaller(workspaceCtx)
	require.NoError(t, err)
	require.Same(t, ownerAttachable, caller,
		"session gateway must route from immutable owner metadata to the owner attachable")

	queryGateway, err := srv.Engine(workspaceCtx)
	require.NoError(t, err)
	require.Same(t, gateway, queryGateway,
		"workspace Query.Engine paths must use the session-owned gateway")
	caller, err = queryGateway.GetSessionCaller(workspaceCtx)
	require.NoError(t, err)
	require.Same(t, ownerAttachable, caller)

	workspaceCtx, _, err = srv.workspaceOwnerAccess(callerCtx, sess, &core.Workspace{ClientID: owner.clientID})
	require.NoError(t, err)
	ownerMetadata, err = engine.ClientMetadataFromContext(workspaceCtx)
	require.NoError(t, err)
	require.Equal(t, "record", ownerMetadata.Labels["source"],
		"workspace access must clone immutable owner metadata")
}

func TestClientScopeRequiresSealedMetadata(t *testing.T) {
	t.Parallel()

	client := &clientRuntime{clientRecord: &clientRecord{
		clientID:       "client",
		clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "client", ClientSecretToken: "token"},
		accepting:      true},
		lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	sess := &daggerSession{
		sessionID:      "session",
		clientRuntimes: map[string]*clientRuntime{client.clientID: client},
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	_, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "before-seal")
	require.ErrorContains(t, err, "not sealed")

	require.NoError(t, sealClientMetadataForTest(sess, client))
	scope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "after-seal")
	require.NoError(t, err)
	scope.Lease().Release()
}

func TestClientLifecycleScopesSerializeCloseCloneAndRelease(t *testing.T) {
	t.Parallel()

	client := &clientRuntime{clientRecord: &clientRecord{
		clientID:       "client",
		clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "client", Labels: map[string]string{"scope": "sealed"}},
		metadataSealed: true,
		accepting:      true},
		state:           clientStateInitialized,
		dagqlRoot:       &core.Query{},
		workspace:       &core.Workspace{},
		failedModules:   map[string]error{"old": errors.New("old")},
		lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	sess := &daggerSession{
		sessionID:          "session",
		mainClientCallerID: client.clientID,
		clientRuntimes:     map[string]*clientRuntime{client.clientID: client},
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)

	transportScope, err := sess.acquireRootClientScope(client, engine.ClientLeaseTransport, "proxy")
	require.NoError(t, err)
	client.transportLease = transportScope.Lease()
	requestScope, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "POST /query")
	require.NoError(t, err)

	sess.closeClientScope(client)
	_, err = sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "late root")
	require.ErrorContains(t, err, "closed")

	// The accepted request remains a strict capability while reachability is
	// closing and may still delegate terminal/background work.
	agentScope, err := requestScope.Clone(engine.ClientLeaseAgent, "agent-1")
	require.NoError(t, err)
	tombstoneScope, err := requestScope.Clone(engine.ClientLeaseAgentTombstone, "agent-tombstone-1")
	require.NoError(t, err)
	serviceScope, err := requestScope.Clone(engine.ClientLeaseService, "service-1")
	require.NoError(t, err)
	sharedScope, err := requestScope.Clone(engine.ClientLeaseSharedWork, "call-1")
	require.NoError(t, err)

	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}
	snapshot := srv.ClientLifecycleDebugSnapshot()
	require.Equal(t, []LifecycleRetentionReason{
		{Kind: "agent", OwnerID: "agent-1", Count: 1},
		{Kind: "agent-tombstone", OwnerID: "agent-tombstone-1", Count: 1},
		{Kind: "request", OwnerID: "POST /query", Count: 1},
		{Kind: "service", OwnerID: "service-1", Count: 1},
		{Kind: "shared-work", OwnerID: "call-1", Count: 1},
	}, snapshot.LeaseCounts)
	require.Equal(t, 1, snapshot.ActiveRequests)

	var wg sync.WaitGroup
	for range 64 {
		wg.Add(5)
		go func() {
			defer wg.Done()
			requestScope.Lease().Release()
		}()
		go func() {
			defer wg.Done()
			agentScope.Lease().Release()
		}()
		go func() {
			defer wg.Done()
			tombstoneScope.Lease().Release()
		}()
		go func() {
			defer wg.Done()
			serviceScope.Lease().Release()
		}()
		go func() {
			defer wg.Done()
			sharedScope.Lease().Release()
		}()
	}
	wg.Wait()
	require.Empty(t, srv.ClientLifecycleDebugSnapshot().LeaseCounts)
	sess.clientMu.RLock()
	_, runtimeRetained := sess.clientRuntimes[client.clientID]
	_, recordRetained := sess.clientRecords[client.clientID]
	sess.clientMu.RUnlock()
	require.False(t, runtimeRetained)
	require.True(t, recordRetained)
	require.Equal(t, clientStateReclaimed, client.state)
	require.Nil(t, client.dagqlRoot, "reclamation must clear execution graph roots from stale lease shells")
	require.Nil(t, client.workspace)
	require.Nil(t, client.failedModules)
	_, err = requestScope.Clone(engine.ClientLeaseChild, "late-child")
	require.ErrorContains(t, err, "not held")
}

func TestClientLifecycleScopesRaceSessionTeardown(t *testing.T) {
	t.Parallel()

	client := &clientRuntime{clientRecord: &clientRecord{
		clientID:       "client",
		clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "client"},
		metadataSealed: true,
		accepting:      true},
		state:           clientStateInitialized,
		lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	sess := &daggerSession{
		sessionID:      "session",
		clientRuntimes: map[string]*clientRuntime{client.clientID: client},
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	transport, err := sess.acquireRootClientScope(client, engine.ClientLeaseTransport, "proxy")
	require.NoError(t, err)
	client.transportLease = transport.Lease()
	source, err := sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "request")
	require.NoError(t, err)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			clone, err := source.Clone(engine.ClientLeaseChild, fmt.Sprintf("child-%d", i))
			if err == nil {
				clone.Lease().Release()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		sess.markSessionRemoved()
		sess.beginClientScopeTeardown()
	}()
	close(start)
	wg.Wait()
	source.Lease().Release()

	_, err = sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "late")
	require.ErrorContains(t, err, "session")
	_, _, leases := sess.clientLifecycleSnapshot(client)
	require.Empty(t, leases)
}

func TestClientRuntimeReclamationRacesCloseLeasesChildAndTeardown(t *testing.T) {
	t.Parallel()

	for range 100 {
		srv, sess, parent, ctx, parentRequest := newNestedTransportTestFixture(t)
		childTransport, err := srv.RegisterNestedClientTransport(ctx, nestedTransportTestMetadata("child"), parent.clientID)
		require.NoError(t, err)
		child := sess.clientRuntimes["child"]

		sess.scopeMu.Lock()
		childRequest := sess.newClientLifecycleLeaseLocked(child, engine.ClientLeaseRequest, "request")
		childService := sess.newClientLifecycleLeaseLocked(child, engine.ClientLeaseService, "service")
		childShared := sess.newClientLifecycleLeaseLocked(child, engine.ClientLeaseSharedWork, "shared")
		sess.scopeMu.Unlock()

		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, action := range []func(){
			func() { childTransport.Close() },
			func() { sess.closeClientScope(parent) },
			func() { parentRequest.Lease().Release() },
			func() { childRequest.Release() },
			func() { childService.Release() },
			func() { childShared.Release() },
			func() {
				sess.lifecycleMu.Lock()
				if sess.state.Load() == sessionStateInitialized {
					sess.markSessionRemoved()
					sess.beginClientScopeTeardown()
				}
				sess.lifecycleMu.Unlock()
			},
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				action()
			}()
		}
		close(start)
		wg.Wait()

		sess.scopeMu.Lock()
		quiescentBeforeRepeat := map[*clientRuntime]time.Time{
			parent: parent.quiescentAt,
			child:  child.quiescentAt,
		}
		sess.scopeMu.Unlock()

		// Repeat every idempotent edge after the race. No lease may remain and a
		// runtime that won live-session reclamation must have exactly one stable
		// quiescent timestamp; teardown is allowed to retain runtime shells until
		// the session object itself is dropped.
		childTransport.Close()
		sess.closeClientScope(parent)
		parentRequest.Lease().Release()
		childRequest.Release()
		childService.Release()
		childShared.Release()
		sess.beginClientScopeTeardown()

		for _, runtime := range []*clientRuntime{parent, child} {
			_, _, leases := sess.clientLifecycleSnapshot(runtime)
			require.Empty(t, leases)
			sess.scopeMu.Lock()
			quiescentAt := runtime.quiescentAt
			sess.scopeMu.Unlock()
			if !quiescentAt.IsZero() {
				require.Equal(t, quiescentBeforeRepeat[runtime], quiescentAt,
					"idempotent cleanup must not repeat the quiescent transition")
				runtime.stateMu.RLock()
				require.Equal(t, clientStateReclaimed, runtime.state)
				runtime.stateMu.RUnlock()
			}
		}
		require.Contains(t, sess.clientRecords, parent.clientID)
		require.Contains(t, sess.clientRecords, child.clientID)
	}
}

func TestClientLifecycleDebugSnapshotReportsClosedRuntimeRetention(t *testing.T) {
	t.Parallel()

	closedAt := time.Now().Add(-time.Minute).Round(0)
	parent := &clientRuntime{clientRecord: &clientRecord{
		clientID:   "parent",
		shutdownAt: closedAt},
		state:       clientStateInitialized,
		activeCount: 2}
	child := &clientRuntime{clientRecord: &clientRecord{
		clientID:        "child",
		parentClientIDs: []string{"parent"}},
		state: clientStateInitialized}
	sess := &daggerSession{
		sessionID: "session",
		clientRuntimes: map[string]*clientRuntime{
			parent.clientID: parent,
			child.clientID:  child,
		},
		telemetryDebug: LifecycleTelemetryCounts{
			TracerProviders:          1,
			LoggerProviders:          1,
			MeterProviders:           1,
			ConfiguredSpanProcessors: 4,
			ConfiguredLogProcessors:  2,
			ConfiguredMetricReaders:  1,
			ConfiguredSpanQueueSlots: enginetel.LargeSpanQueueSize,
			ConfiguredLogQueueSlots:  enginetel.LogQueueSize,
		},
	}
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}

	snapshot := srv.ClientLifecycleDebugSnapshot()
	require.Equal(t, 2, snapshot.Records)
	require.Equal(t, 2, snapshot.Runtimes)
	require.Equal(t, 1, snapshot.ClosedRuntimes)
	require.Equal(t, 2, snapshot.ActiveRequests)
	require.NotNil(t, snapshot.OldestClosedRuntime)
	require.Equal(t, closedAt, *snapshot.OldestClosedRuntime)
	require.Equal(t, 1, snapshot.Providers.TracerProviders)
	require.Equal(t, 1, snapshot.Providers.MeterProviders)
	require.Equal(t, 1, snapshot.Providers.ConfiguredMetricReaders)
	require.Equal(t, 4, snapshot.Providers.ConfiguredSpanProcessors)
	require.False(t, snapshot.Providers.QueueOccupancyMeasured)

	require.Len(t, snapshot.Sessions, 1)
	require.Equal(t, []string{"child", "parent"}, []string{
		snapshot.Sessions[0].Clients[0].ClientID,
		snapshot.Sessions[0].Clients[1].ClientID,
	})
	gotParent := snapshot.Sessions[0].Clients[1]
	require.Equal(t, "shutdown-signaled", gotParent.RecordState)
	require.Equal(t, "closed-retained", gotParent.RuntimeState)
	require.Zero(t, gotParent.Telemetry.MeterProviders,
		"client runtimes must not report metric provider ownership")
	require.Zero(t, gotParent.Telemetry.ConfiguredMetricReaders)
	require.False(t, gotParent.MetadataSealed)
	require.ElementsMatch(t, []string{"lifecycle-transition", "request"}, []string{
		gotParent.RetentionReasons[0].Kind,
		gotParent.RetentionReasons[1].Kind,
	})
}

func TestClientAncestryAndTelemetryRouteOrdering(t *testing.T) {
	t.Parallel()

	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root"}}
	parent := &clientRuntime{clientRecord: &clientRecord{clientID: "parent", parentClientIDs: []string{"root"}}}
	child := &clientRuntime{clientRecord: &clientRecord{clientID: "child", parentClientIDs: []string{"root", "parent"}}}
	sess := &daggerSession{clientRuntimes: map[string]*clientRuntime{
		root.clientID:   root,
		parent.clientID: parent,
		child.clientID:  child,
	}}
	for _, client := range sess.clientRuntimes {
		client.daggerSession = sess
	}
	installTestClientRecords(sess)

	ancestors, err := sess.ancestorRuntimes(child.clientRecord)
	require.NoError(t, err)
	require.Equal(t, []string{"root", "parent"}, []string{ancestors[0].clientID, ancestors[1].clientID})

	route, err := sess.telemetryRouteClientIDs(child.clientRecord)
	require.NoError(t, err)
	require.Equal(t, []string{"child", "root", "parent"}, route,
		"telemetry must preserve origin-first, root-to-direct-parent fan-out")

	delivery, err := sess.telemetryDeliveryClientIDs(child.clientRecord)
	require.NoError(t, err)
	require.Equal(t, []string{"root", "parent", "child"}, delivery,
		"call-payload claims retain their historical ancestor-first ordering")

	route[1] = "mutated"
	require.Equal(t, []string{"root", "parent"}, child.parentClientIDs,
		"returned routes must not alias immutable client ancestry")
}

func TestMetricAttributesPreferImmutableClientScopeOrigin(t *testing.T) {
	t.Parallel()

	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test", nil, nil)
	defer lease.Release()
	scope, err := engine.NewClientScope(&engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "scope-origin",
	}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "mutable-metadata",
	})

	config := metric.NewRecordConfig([]metric.RecordOption{
		telemetryattrs.MetricAttributes(ctx,
			attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, "payload-claim")),
	})
	origin, err := metricDataOriginClientID(config.Attributes())
	require.NoError(t, err)
	require.Equal(t, "scope-origin", origin)
}

func TestSessionTelemetryRoutesOriginAndAncestorsExactlyOnce(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs, wcprofSpanCount: newWcprofSpanCounter()}
	srv.telemetryPubSub = NewPubSub(srv)

	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root", clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "root"}}}
	parent := &clientRuntime{clientRecord: &clientRecord{clientID: "parent", parentClientIDs: []string{"root"}, clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "parent"}}}
	child := &clientRuntime{clientRecord: &clientRecord{clientID: "child", parentClientIDs: []string{"root", "parent"}, clientMetadata: &engine.ClientMetadata{SessionID: "session", ClientID: "child"}, shutdownCh: make(chan struct{})}}
	sess := &daggerSession{
		sessionID:          "session",
		mainClientCallerID: root.clientID,
		clientRuntimes: map[string]*clientRuntime{
			root.clientID: root, parent.clientID: parent, child.clientID: child,
		},
		telemetryPubSub: srv.telemetryPubSub,
	}
	for _, client := range sess.clientRuntimes {
		client.daggerSession = sess
	}
	installTestClientRecords(sess)
	srv.initializeSessionTelemetry(sess)
	t.Cleanup(func() { require.NoError(t, sess.shutdownTelemetry(context.Background())) })
	workGauge, err := sess.meterProvider.Meter("test").Int64Gauge("test.client.work")
	require.NoError(t, err)

	emit := func(client *clientRuntime, name string, detached bool) trace.SpanID {
		ctx := engine.ContextWithClientMetadata(context.Background(), client.clientMetadata)
		ctx = telemetry.WithLoggerProvider(ctx, sess.loggerProvider)
		if detached {
			ctx = context.WithoutCancel(ctx)
			client.closeShutdownOnce.Do(func() { close(client.shutdownCh) })
		}
		_, span := sess.tracerProvider.Tracer("test").Start(ctx, name)
		spanID := span.SpanContext().SpanID()
		span.End()
		rec := otellog.Record{}
		rec.SetTimestamp(time.Now())
		rec.SetBody(otellog.StringValue(name))
		telemetry.Logger(ctx, "test").Emit(ctx, rec)
		workGauge.Record(ctx, 1, telemetryattrs.MetricAttributes(ctx))
		return spanID
	}

	rootSpanID := emit(root, "root-work", false)
	childSpanID := emit(child, "detached-child-work", true)
	require.NoError(t, sess.FlushTelemetry(t.Context(), "test"))

	load := func(clientID string) ([]clientdb.Span, []clientdb.Log, []clientdb.Metric) {
		db, err := dbs.Open(t.Context(), clientID)
		require.NoError(t, err)
		defer db.Close()
		spans, err := db.Read().SelectSpansSince(t.Context(), clientdb.SelectSpansSinceParams{Limit: 100})
		require.NoError(t, err)
		logs, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
		require.NoError(t, err)
		metrics, err := db.Read().SelectMetricsSince(t.Context(), clientdb.SelectMetricsSinceParams{Limit: 100})
		require.NoError(t, err)
		return spans, logs, metrics
	}
	countSpan := func(spans []clientdb.Span, id trace.SpanID) int {
		count := 0
		for _, span := range spans {
			if span.SpanID == id.String() {
				count++
			}
		}
		return count
	}

	originAttr := func(attrsJSON []byte) string {
		var attrs []*otlpcommonv1.KeyValue
		require.NoError(t, clientdb.UnmarshalProtoJSONs(attrsJSON, &otlpcommonv1.KeyValue{}, &attrs))
		for _, attr := range attrs {
			if attr.Key == telemetryattrs.TelemetryOriginClientIDAttr {
				return attr.Value.GetStringValue()
			}
		}
		return ""
	}
	metricPointCount := func(rows []clientdb.Metric) int {
		count := 0
		for _, resourceMetricsPB := range clientdb.MetricsToPB(rows) {
			resourceMetrics, err := telemetry.ResourceMetricsFromPB(resourceMetricsPB)
			require.NoError(t, err)
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				for _, metrics := range scopeMetrics.Metrics {
					gauge, ok := metrics.Data.(metricdata.Gauge[int64])
					require.True(t, ok, "unexpected metric aggregation %T", metrics.Data)
					for _, point := range gauge.DataPoints {
						_, hasOrigin := point.Attributes.Value(attribute.Key(telemetryattrs.TelemetryOriginClientIDAttr))
						require.False(t, hasOrigin, "routing-only origin must not be persisted")
						count++
					}
				}
			}
		}
		return count
	}

	rootSpans, rootLogs, rootMetrics := load("root")
	parentSpans, parentLogs, parentMetrics := load("parent")
	childSpans, childLogs, childMetrics := load("child")
	// Live span export emits one start and one end snapshot. Each snapshot must
	// reach each visibility target once, without duplicate ancestry delivery.
	require.Equal(t, 2, countSpan(rootSpans, rootSpanID))
	require.Equal(t, 2, countSpan(rootSpans, childSpanID))
	require.Equal(t, 2, countSpan(parentSpans, childSpanID))
	require.Equal(t, 2, countSpan(childSpans, childSpanID))
	require.Zero(t, countSpan(parentSpans, rootSpanID))
	require.Zero(t, countSpan(childSpans, rootSpanID))
	require.Len(t, rootLogs, 2)
	require.Len(t, parentLogs, 1)
	require.Len(t, childLogs, 1)
	require.Equal(t, 2, metricPointCount(rootMetrics))
	require.Equal(t, 1, metricPointCount(parentMetrics))
	require.Equal(t, 1, metricPointCount(childMetrics))
	for _, span := range append(append(rootSpans, parentSpans...), childSpans...) {
		require.Empty(t, originAttr(span.Attributes), "routing-only span origin must not be persisted")
	}
	for _, row := range append(append(rootLogs, parentLogs...), childLogs...) {
		require.Empty(t, originAttr(row.Attributes), "routing-only log origin must not be persisted")
	}

	// Incoming OTLP/cloud telemetry has no emission context. Its authenticated
	// origin adapter must overwrite any payload claim and use the same route.
	require.NoError(t, (originSpanExporter{origin: "parent", next: sess.spanExporter}).ExportSpans(
		t.Context(), []sdktrace.ReadOnlySpan{childSpans[0].ReadOnly()}))
	incomingLog := sdklog.Record{}
	incomingLog.AddAttributes(otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "child"))
	require.NoError(t, (originLogExporter{origin: "parent", next: sess.logExporter}).Export(
		t.Context(), []sdklog.Record{incomingLog}))
	incomingMetrics := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{{
			Name: "incoming",
			Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
				Attributes: attribute.NewSet(attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, "child")),
				Value:      1,
			}}},
		}},
	}}}
	require.NoError(t, (originMetricExporter{origin: "parent", next: sess.metricExporter}).Export(
		t.Context(), incomingMetrics))
	rootSpans, rootLogs, rootMetrics = load("root")
	parentSpans, parentLogs, parentMetrics = load("parent")
	childSpans, childLogs, childMetrics = load("child")
	require.Equal(t, 3, countSpan(rootSpans, childSpanID))
	require.Equal(t, 3, countSpan(parentSpans, childSpanID))
	require.Equal(t, 2, countSpan(childSpans, childSpanID))
	require.Len(t, rootLogs, 3)
	require.Len(t, parentLogs, 2)
	require.Len(t, childLogs, 1)
	require.Equal(t, 3, metricPointCount(rootMetrics))
	require.Equal(t, 2, metricPointCount(parentMetrics))
	require.Equal(t, 1, metricPointCount(childMetrics))
	require.Empty(t, originAttr(rootLogs[len(rootLogs)-1].Attributes))

	require.Equal(t, 0, dbs.OpenStats().Stores, "exports and reads must release DB handles")

	srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
	sess.state.Store(sessionStateInitialized)
	snapshot := srv.ClientLifecycleDebugSnapshot()
	require.Equal(t, 1, snapshot.Providers.TracerProviders)
	require.Equal(t, 1, snapshot.Providers.LoggerProviders)
	require.Equal(t, 1, snapshot.Providers.MeterProviders)
	require.Equal(t, 1, snapshot.Providers.ConfiguredMetricReaders)
	require.Equal(t, enginetel.LargeSpanQueueSize, snapshot.Providers.ConfiguredSpanQueueSlots)
	require.Equal(t, enginetel.LogQueueSize, snapshot.Providers.ConfiguredLogQueueSlots)
	for _, clientSnapshot := range snapshot.Sessions[0].Clients {
		require.Zero(t, clientSnapshot.Telemetry.MeterProviders)
		require.Zero(t, clientSnapshot.Telemetry.ConfiguredMetricReaders)
	}
}

func TestClientRegistrationRequiresValidExplicitAncestry(t *testing.T) {
	t.Parallel()

	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root"}}
	otherRoot := &clientRuntime{clientRecord: &clientRecord{clientID: "other-root"}}
	child := &clientRuntime{clientRecord: &clientRecord{clientID: "child", parentClientIDs: []string{"root"}}}
	sess := &daggerSession{clientRuntimes: map[string]*clientRuntime{
		root.clientID:      root,
		otherRoot.clientID: otherRoot,
		child.clientID:     child,
	}}
	installTestClientRecords(sess)

	parentIDs, err := sess.parentClientIDsForRegistration("new-root", "", true)
	require.NoError(t, err)
	require.Empty(t, parentIDs)

	_, err = sess.parentClientIDsForRegistration("implicit-root", "", false)
	require.ErrorContains(t, err, "missing parent client ID")

	_, err = sess.parentClientIDsForRegistration("nested", "missing", false)
	require.ErrorContains(t, err, `parent client "missing" not found`)

	_, err = sess.parentClientIDsForRegistration("cycle", "cycle", false)
	require.ErrorContains(t, err, "ancestry cycle")

	_, err = sess.parentClientIDsForRegistration("child", "other-root", false)
	require.ErrorContains(t, err, "different parent ancestry")

	_, err = sess.parentClientIDsForRegistration("child", "", true)
	require.ErrorContains(t, err, "different parent ancestry")
}

func TestClientRegistrationRejectsInvalidParentRoute(t *testing.T) {
	t.Parallel()

	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root"}}
	broken := &clientRuntime{clientRecord: &clientRecord{clientID: "broken", parentClientIDs: []string{"missing"}}}
	sess := &daggerSession{clientRuntimes: map[string]*clientRuntime{
		root.clientID:   root,
		broken.clientID: broken,
	}}
	installTestClientRecords(sess)

	_, err := sess.parentClientIDsForRegistration("child", "broken", false)
	require.ErrorContains(t, err, `ancestor client "missing" not found`)

	broken.parentClientIDs = []string{"root", "root"}
	_, err = sess.parentClientIDsForRegistration("child", "broken", false)
	require.ErrorContains(t, err, "reaches \"root\" more than once")
}

func TestLifecycleDebugAndRouteLookupConcurrentClientMutation(t *testing.T) {
	t.Parallel()

	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root"}, state: clientStateInitialized}
	sess := &daggerSession{
		sessionID:          "session",
		mainClientCallerID: root.clientID,
		clientRuntimes:     map[string]*clientRuntime{root.clientID: root},
	}
	root.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			child := &clientRuntime{clientRecord: &clientRecord{
				clientID:        "transient",
				daggerSession:   sess,
				parentClientIDs: []string{root.clientID}},
				state: clientStateInitialized}
			sess.clientMu.Lock()
			sess.clientRecords[child.clientID] = child.clientRecord
			sess.clientRuntimes[child.clientID] = child
			sess.clientMu.Unlock()
			_, _ = sess.telemetryRouteClientIDs(child.clientRecord)
			sess.clientMu.Lock()
			delete(sess.clientRecords, child.clientID)
			delete(sess.clientRuntimes, child.clientID)
			sess.clientMu.Unlock()
		}
	}()

	for range 1000 {
		snapshot := srv.ClientLifecycleDebugSnapshot()
		require.Len(t, snapshot.Sessions, 1)
		got, err := srv.clientRecordFromIDs(sess.sessionID, root.clientID)
		require.NoError(t, err)
		route, err := sess.telemetryRouteClientIDs(got)
		require.NoError(t, err)
		require.Equal(t, []string{root.clientID}, route)
	}
	cancel()
	wg.Wait()
}

type fakeSessionCaller struct {
	id   string
	conn *grpc.ClientConn
}

func TestLogRecordRowPreservesBytesBody(t *testing.T) {
	payload := []byte{0, 1, 2, 0xff}
	var record sdklog.Record
	record.SetBody(otellog.BytesValue(payload))

	row, err := logRecordRow(&record)
	require.NoError(t, err)

	var body otlpcommonv1.AnyValue
	require.NoError(t, proto.Unmarshal(row.Body, &body))
	require.Equal(t, payload, body.GetBytesValue())
}

func TestTelemetryExportReleasesClientDBHandle(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	ps := &PubSub{srv: &Server{clientDBs: dbs}}

	records := []sdklog.Record{{}}
	require.NoError(t, ps.Logs("client").Export(t.Context(), records))
	stats := dbs.OpenStats()
	require.Zero(t, stats.Stores)
	require.Zero(t, stats.Streams)
	require.Zero(t, stats.Refs)
}

func (caller *fakeSessionCaller) Supports(string) bool {
	return false
}

func (caller *fakeSessionCaller) Conn() *grpc.ClientConn {
	return caller.conn
}

func TestActiveClientIDsConcurrentSessionClientMutation(t *testing.T) {
	t.Parallel()

	// Regression test: activeClientIDs must read sess.clientRecords under clientMu.
	// Without the lock, ranging the map while another goroutine writes it is a
	// fatal "concurrent map iteration and map write" (caught here under -race).
	sess := &daggerSession{
		clientRecords: map[string]*clientRecord{
			"client-a": {clientID: "client-a"},
		},
	}
	sess.state.Store(sessionStateInitialized)
	srv := &Server{
		daggerSessions: map[string]*daggerSession{
			"session-a": sess,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sess.clientMu.Lock()
			sess.clientRecords["transient"] = &clientRecord{clientID: "transient"}
			delete(sess.clientRecords, "transient")
			sess.clientMu.Unlock()
		}
	}()
	<-started

	for i := 0; i < 1000; i++ {
		require.True(t, srv.activeClientIDs()["client-a"])
	}
}

func TestClientRecordFromIDsConcurrentSessionInitialization(t *testing.T) {
	t.Parallel()

	// Regression test: clientRecordFromIDs must read sess.state (atomically) and
	// sess.clientRecords (under clientMu) while another goroutine mutates them during
	// session initialization. Without that discipline this is a data race (caught
	// here under -race).
	sess := &daggerSession{}
	sess.state.Store(sessionStateUninitialized)
	srv := &Server{
		daggerSessions: map[string]*daggerSession{
			"session-a": sess,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, _ = srv.clientRecordFromIDs("session-a", "client-a")
		}
	}()
	<-started

	for i := 0; i < 1000; i++ {
		sess.clientMu.Lock()
		sess.clientRecords = map[string]*clientRecord{
			"client-a": {clientID: "client-a"},
		}
		sess.clientMu.Unlock()
		sess.state.Store(sessionStateInitialized)
		sess.state.Store(sessionStateUninitialized)
		sess.clientMu.Lock()
		sess.clientRecords = nil
		sess.clientMu.Unlock()
	}

	record := &clientRecord{clientID: "client-a", daggerSession: sess}
	sess.clientMu.Lock()
	sess.clientRecords = map[string]*clientRecord{
		record.clientID: record,
	}
	sess.clientMu.Unlock()
	sess.state.Store(sessionStateInitialized)

	got, err := srv.clientRecordFromIDs("session-a", record.clientID)
	require.NoError(t, err)
	require.Same(t, record, got)
}

func TestClientsDoesNotBlockWhileSessionLifecycleLocked(t *testing.T) {
	t.Parallel()

	// Regression for the >15s active-clients stall (Discord: "Session lock might
	// be causing unwanted session shutdowns"): Clients() must never acquire a
	// session's lifecycleMu. A session stuck initializing or tearing down holds
	// lifecycleMu for a long time (teardown has a 60s safeguard), but that must
	// not stall the active-clients API the cloud keepalive polls.
	live := &daggerSession{sessionID: "live", mainClientCallerID: "main-live"}
	live.state.Store(sessionStateInitialized)
	busy := &daggerSession{sessionID: "busy", mainClientCallerID: "main-busy"}
	busy.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{
		"live": live,
		"busy": busy,
	}}

	// Simulate an in-progress init/teardown holding busy's lifecycleMu.
	busy.lifecycleMu.Lock()
	defer busy.lifecycleMu.Unlock()

	done := make(chan []string, 1)
	go func() { done <- srv.Clients() }()

	select {
	case clients := <-done:
		require.ElementsMatch(t, []string{"main-live", "main-busy"}, clients)
	case <-time.After(10 * time.Second):
		t.Fatal("Clients() blocked while a session's lifecycleMu was held")
	}
}

func TestActiveClientIDsDoesNotBlockWhileSessionLifecycleLocked(t *testing.T) {
	t.Parallel()

	// activeClientIDs() (the client-DB GC ticker) must also never acquire a
	// session's lifecycleMu, for the same reason as Clients().
	live := &daggerSession{
		sessionID:     "live",
		clientRecords: map[string]*clientRecord{"c-live": {clientID: "c-live"}},
	}
	live.state.Store(sessionStateInitialized)
	busy := &daggerSession{
		sessionID:     "busy",
		clientRecords: map[string]*clientRecord{"c-busy": {clientID: "c-busy"}},
	}
	busy.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{
		"live": live,
		"busy": busy,
	}}

	busy.lifecycleMu.Lock()
	defer busy.lifecycleMu.Unlock()

	done := make(chan map[string]bool, 1)
	go func() { done <- srv.activeClientIDs() }()

	select {
	case keep := <-done:
		require.True(t, keep["c-live"], "expected live session's client to be kept")
		require.True(t, keep["c-busy"], "expected initialized busy session's client to be kept")
	case <-time.After(10 * time.Second):
		t.Fatal("activeClientIDs() blocked while a session's lifecycleMu was held")
	}
}

func TestGetOrInitClientReturnsFastForRemovedTombstone(t *testing.T) {
	t.Parallel()

	// A session mid-teardown holds lifecycleMu and is marked removed (a tombstone
	// left in the registry until cleanup completes). A same-id getOrInitClient
	// must bail immediately via the lock-free removed pre-check rather than block
	// on lifecycleMu for the (possibly ~60s) teardown.
	tombstone := &daggerSession{sessionID: "s", mainClientCallerID: "m"}
	tombstone.state.Store(sessionStateRemoved)
	srv := &Server{daggerSessions: map[string]*daggerSession{"s": tombstone}}

	// Hold lifecycleMu to simulate an in-progress teardown.
	tombstone.lifecycleMu.Lock()
	defer tombstone.lifecycleMu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
			ClientMetadata: &engine.ClientMetadata{
				SessionID:         "s",
				ClientID:          "m",
				ClientSecretToken: "token",
			},
		})
		done <- err
	}()

	select {
	case err := <-done:
		var retryable flightcontrol.RetryableError
		require.ErrorAs(t, err, &retryable, "removed tombstone should yield a retryable error")
	case <-time.After(10 * time.Second):
		t.Fatal("getOrInitClient blocked on lifecycleMu for a removed tombstone")
	}
}

func TestClientRecordFromIDsStateGating(t *testing.T) {
	t.Parallel()

	// clientRecordFromIDs gates on the session's atomic lifecycle state without
	// ever taking lifecycleMu, and never returns a record whose session is not
	// usable.
	client := &clientRuntime{clientRecord: &clientRecord{clientID: "c"}}
	sess := &daggerSession{
		sessionID:      "s",
		clientRecords:  map[string]*clientRecord{"c": client.clientRecord},
		clientRuntimes: map[string]*clientRuntime{"c": client},
	}
	client.daggerSession = sess
	srv := &Server{daggerSessions: map[string]*daggerSession{"s": sess}}

	// uninitialized: not yet usable.
	sess.state.Store(sessionStateUninitialized)
	_, err := srv.clientRecordFromIDs("s", "c")
	require.ErrorContains(t, err, "not initialized")

	// removed: retryable not-found (session is tearing down).
	sess.state.Store(sessionStateRemoved)
	_, err = srv.clientRecordFromIDs("s", "c")
	var retryable flightcontrol.RetryableError
	require.ErrorAs(t, err, &retryable)

	// initialized: returns the client.
	sess.state.Store(sessionStateInitialized)
	got, err := srv.clientRecordFromIDs("s", "c")
	require.NoError(t, err)
	require.Same(t, client.clientRecord, got)
}

func TestSessionLifecycleObserverConcurrency(t *testing.T) {
	t.Parallel()

	// Stress the observer paths (Clients/activeClientIDs/clientRecordFromIDs) against
	// concurrent session churn. The churners exercise the observer-visible state
	// the way the real lifecycle does — registry writes under daggerSessionsMu,
	// the clients map under clientMu, the lifecycle state via the atomic, and a
	// pointer-conditional deleteSession on teardown — but deliberately do NOT take
	// lifecycleMu, since the whole point of the redesign is that observers don't
	// depend on it. Run under -race to catch data races; the observers must also
	// never block (completing while churn runs is the liveness assertion).
	srv := &Server{daggerSessions: map[string]*daggerSession{}}

	const (
		churners         = 4
		cyclesPerChurner = 1000
	)
	var wg sync.WaitGroup
	for i := range churners {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("s%d", n)
			for range cyclesPerChurner {
				sess := &daggerSession{
					sessionID:          id,
					mainClientCallerID: "m" + id,
					clientRecords:      map[string]*clientRecord{},
					clientRuntimes:     map[string]*clientRuntime{},
				}
				// publish, then populate clients, then flip to initialized last.
				srv.daggerSessionsMu.Lock()
				srv.daggerSessions[id] = sess
				srv.daggerSessionsMu.Unlock()
				sess.clientMu.Lock()
				record := &clientRecord{clientID: "c", daggerSession: sess}
				sess.clientRecords[record.clientID] = record
				sess.clientRuntimes[record.clientID] = &clientRuntime{clientRecord: record}
				sess.clientMu.Unlock()
				sess.state.Store(sessionStateInitialized)

				// teardown: removed first, then pointer-conditional delete.
				sess.state.Store(sessionStateRemoved)
				srv.deleteSession(sess)
			}
		}(i)
	}

	churnDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(churnDone)
	}()

	// Hammer the observers concurrently until every churner has finished its
	// fixed workload, so the race window is exercised deterministically rather
	// than depending on scheduler timing.
	for {
		select {
		case <-churnDone:
			return
		default:
		}
		_ = srv.Clients()
		_ = srv.activeClientIDs()
		_, _ = srv.clientRecordFromIDs("s0", "c")
	}
}

// newTeardownTestServer builds a Server with just enough real state for
// removeDaggerSession to run: an empty in-memory dagql cache, a live wcprof
// counter, and a stubbed GC callback (scheduled via time.AfterFunc at the end
// of teardown).
func newTeardownTestServer(t *testing.T) *Server {
	t.Helper()
	cache, err := dagql.NewCache(context.Background(), "", nil, nil)
	require.NoError(t, err)
	return &Server{
		daggerSessions:     map[string]*daggerSession{},
		releasedSessionIDs: map[string]struct{}{},
		engineCache:        cache,
		wcprofSpanCount:    newWcprofSpanCounter(),
		throttledSessionGC: func() {},
	}
}

type cleanupSpanMetricExporter struct {
	ctx      context.Context
	tracer   trace.Tracer
	exported chan<- struct{}
}

func (exp cleanupSpanMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	if exp.exported != nil {
		select {
		case exp.exported <- struct{}{}:
		default:
		}
	}
	return nil
}
func (cleanupSpanMetricExporter) ForceFlush(context.Context) error { return nil }
func (exp cleanupSpanMetricExporter) Shutdown(context.Context) error {
	_, span := exp.tracer.Start(exp.ctx, "metric cleanup telemetry")
	span.End()
	return nil
}
func (cleanupSpanMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}
func (cleanupSpanMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func TestSessionTeardownFlushesTraceTelemetryAfterMetricShutdown(t *testing.T) {
	srv := newTeardownTestServer(t)
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.telemetryPubSub = NewPubSub(srv)

	md := &engine.ClientMetadata{SessionID: "session", ClientID: "main"}
	client := &clientRuntime{clientRecord: &clientRecord{clientID: "main", clientMetadata: md, shutdownCh: make(chan struct{})}}
	sess := &daggerSession{
		sessionID:          md.SessionID,
		mainClientCallerID: md.ClientID,
		clientRuntimes:     map[string]*clientRuntime{client.clientID: client},
		services:           core.NewServices(),
		analytics:          analytics.New(analytics.Config{DoNotTrack: true}),
		containers:         map[bkgw.Container]struct{}{},
		shutdownCh:         make(chan struct{}),
		telemetryPubSub:    srv.telemetryPubSub,
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	srv.initializeSessionTelemetry(sess)

	cleanupCtx := engine.ContextWithClientMetadata(context.Background(), md)
	exported := make(chan struct{}, 1)
	exporter := cleanupSpanMetricExporter{
		ctx:      cleanupCtx,
		tracer:   sess.tracerProvider.Tracer("test"),
		exported: exported,
	}
	require.NoError(t, sess.meterProvider.Shutdown(t.Context()))
	sess.meterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour)),
	))
	gauge, err := sess.meterProvider.Meter("test").Int64Gauge("cleanup.metric")
	require.NoError(t, err)
	gauge.Record(cleanupCtx, 1, telemetryattrs.MetricAttributes(cleanupCtx))

	require.NoError(t, srv.removeDaggerSession(t.Context(), sess))
	select {
	case <-exported:
	default:
		t.Fatal("final session telemetry barrier did not flush metrics")
	}
	db, err := srv.clientDBs.Open(t.Context(), client.clientID)
	require.NoError(t, err)
	defer db.Close()
	spans, err := db.Read().SelectSpansSince(t.Context(), clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	var cleanupSnapshots int
	for _, span := range spans {
		if span.Name == "metric cleanup telemetry" {
			cleanupSnapshots++
		}
	}
	require.Equal(t, 2, cleanupSnapshots,
		"cleanup span start/end snapshots must pass the final session flush")
}

// newTeardownTestSession publishes an initialized session whose main client
// has the given number of active connections. dagqlInFlight starts at 1 so
// teardown deterministically blocks in the in-flight drain until
// releaseTeardownDrain is called.
func newTeardownTestSession(srv *Server, sessionID, mainClientID string, activeCount int) (*daggerSession, *clientRuntime) {
	client := &clientRuntime{clientRecord: &clientRecord{
		clientID: mainClientID},
		activeCount: activeCount}
	sess := &daggerSession{
		sessionID:          sessionID,
		mainClientCallerID: mainClientID,
		clientRuntimes:     map[string]*clientRuntime{mainClientID: client},
		services:           core.NewServices(),
		analytics:          analytics.New(analytics.Config{DoNotTrack: true}),
		shutdownCh:         make(chan struct{}),
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.dagqlInFlight = 1
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.state.Store(sessionStateInitialized)

	srv.daggerSessionsMu.Lock()
	srv.daggerSessions[sessionID] = sess
	srv.daggerSessionsMu.Unlock()
	return sess, client
}

func releaseTeardownDrain(sess *daggerSession) {
	sess.dagqlMu.Lock()
	sess.dagqlInFlight = 0
	sess.dagqlCond.Broadcast()
	sess.dagqlMu.Unlock()
}

func sessionInRegistry(srv *Server, sessionID string) bool {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	_, ok := srv.daggerSessions[sessionID]
	return ok
}

func sessionIDReleased(srv *Server, sessionID string) bool {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	_, ok := srv.releasedSessionIDs[sessionID]
	return ok
}

func TestMainClientLastDisconnectDoesNotBlockOnTeardown(t *testing.T) {
	t.Parallel()

	// Regression for the client-side `shutdown: ... context deadline exceeded`
	// timeout: the main client's last connection cleanup runs in the request
	// handler before the /shutdown response is flushed, so it must only
	// SCHEDULE teardown, never run it. Teardown here is deterministically
	// blocked in the in-flight dagql drain; the cleanup must return anyway.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	done := make(chan error, 1)
	go func() {
		done <- srv.releaseClientConnection(context.Background(), sess, client)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("last-connection cleanup blocked on session teardown")
	}

	// The background reap marks the session removed (tombstone) but stays
	// blocked in the drain, so the registry entry must survive for now.
	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond, "background teardown never started")
	require.True(t, sessionInRegistry(srv, "s"), "tombstone dropped before teardown finished")

	releaseTeardownDrain(sess)

	require.Eventually(t, func() bool {
		return !sessionInRegistry(srv, "s")
	}, 10*time.Second, 10*time.Millisecond, "session never finished background teardown")
	require.True(t, sessionIDReleased(srv, "s"))
	select {
	case <-sess.shutdownCh:
	case <-time.After(10 * time.Second):
		t.Fatal("session shutdownCh never closed by background teardown")
	}
}

func TestSameIDConnectDuringBackgroundTeardownGetsRetryable(t *testing.T) {
	t.Parallel()

	// While a background reap is mid-teardown (removed tombstone in the
	// registry, lifecycleMu held), a same-id getOrInitClient must bail out
	// fast with a retryable error instead of blocking on the teardown.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	require.NoError(t, srv.releaseClientConnection(context.Background(), sess, client))
	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
			ClientMetadata: &engine.ClientMetadata{
				SessionID:         "s",
				ClientID:          "m",
				ClientSecretToken: "token",
			},
		})
		done <- err
	}()
	select {
	case err := <-done:
		var retryable flightcontrol.RetryableError
		require.ErrorAs(t, err, &retryable)
	case <-time.After(10 * time.Second):
		t.Fatal("same-id getOrInitClient blocked on background teardown")
	}
	require.False(t, sessionIDReleased(srv, "s"), "session ID retired before teardown completed")

	releaseTeardownDrain(sess)
	require.Eventually(t, func() bool {
		return !sessionInRegistry(srv, "s") && sessionIDReleased(srv, "s")
	}, 10*time.Second, 10*time.Millisecond)

	_, _, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
		ClientMetadata: &engine.ClientMetadata{
			SessionID:         "s",
			ClientID:          "m",
			ClientSecretToken: "token",
		},
	})
	require.ErrorContains(t, err, "already used and released")
	var retryable flightcontrol.RetryableError
	require.False(t, errors.As(err, &retryable))
}

func TestReapAbandonedWhenMainClientReconnects(t *testing.T) {
	t.Parallel()

	// A new main-client connection can land between the last disconnect and
	// the scheduled reap running. The reap re-checks activeCount under
	// lifecycleMu and must leave the now-live session alone.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	// Simulate the reconnect winning the race: activeCount is back above zero
	// by the time the reap runs.
	client.stateMu.Lock()
	client.activeCount = 1
	client.stateMu.Unlock()

	srv.reapDaggerSession(context.Background(), sess, client)

	require.Equal(t, sessionStateInitialized, sess.state.Load())
	require.True(t, sessionInRegistry(srv, "s"))
	require.False(t, sessionIDReleased(srv, "s"))
	select {
	case <-sess.shutdownCh:
		t.Fatal("reap tore down a session with a live main client")
	default:
	}
}

func TestFailedSessionInitializationCanRetrySameID(t *testing.T) {
	t.Parallel()

	srv := &Server{
		daggerSessions:     map[string]*daggerSession{},
		releasedSessionIDs: map[string]struct{}{},
	}
	srv.daggerSessionsMu.Lock()
	failed, created, err := srv.getOrCreateSessionLocked("s", "m")
	srv.daggerSessionsMu.Unlock()
	require.NoError(t, err)
	require.True(t, created)

	// This is the getOrInitClient failed-initialization cleanup: publish removed,
	// release lifecycleMu, then delete without retiring the ID.
	failed.state.Store(sessionStateRemoved)
	failed.lifecycleMu.Unlock()
	srv.deleteSession(failed)
	require.False(t, sessionIDReleased(srv, "s"))

	srv.daggerSessionsMu.Lock()
	retry, created, err := srv.getOrCreateSessionLocked("s", "m")
	srv.daggerSessionsMu.Unlock()
	require.NoError(t, err)
	require.True(t, created)
	require.NotSame(t, failed, retry)
	retry.lifecycleMu.Unlock()
	srv.deleteSession(retry)
}

func TestConcurrentReapsSingleTeardown(t *testing.T) {
	t.Parallel()

	// Duplicate reaps can be scheduled (disconnect, reconnect, disconnect).
	// Whichever acquires lifecycleMu first tears down; the loser must observe
	// the removed state and no-op (double teardown would double-close
	// shutdownCh and panic).
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 0)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.reapDaggerSession(context.Background(), sess, client)
		}()
	}

	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond)
	releaseTeardownDrain(sess)
	wg.Wait()

	require.False(t, sessionInRegistry(srv, "s"))
	select {
	case <-sess.shutdownCh:
	default:
		t.Fatal("session shutdownCh not closed after teardown")
	}
}

func TestPendingLegacyModule(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Cwd: "."}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return "/resolved/" + relPath
	}

	t.Run("preserves remote pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"go",
			"github.com/acme/go-toolchain@main",
			"abc123",
			false,
			map[string]any{"foo": "bar"},
			[]*modules.ModuleConfigArgument{{
				Argument:    "config",
				DefaultPath: "./custom-config.txt",
			}},
		)

		require.Equal(t, "github.com/acme/go-toolchain@main", mod.Ref)
		require.Equal(t, "abc123", mod.RefPin)
		require.Equal(t, "go", mod.Name)
		require.False(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
		require.Equal(t, map[string]any{"foo": "bar"}, mod.ConfigDefaults)
		require.Len(t, mod.ArgCustomizations, 1)
		require.Equal(t, "./custom-config.txt", mod.ArgCustomizations[0].DefaultPath)
	})

	t.Run("resolves local refs without ref pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"blueprint",
			"../blueprint",
			"",
			true,
			nil,
			nil,
		)

		require.Equal(t, "/resolved/../blueprint", mod.Ref)
		require.Empty(t, mod.RefPin)
		require.Equal(t, "blueprint", mod.Name)
		require.True(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
		require.Nil(t, mod.ConfigDefaults)
	})
}

func TestFilterPendingWorkspaceModulesForRootFields(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{Kind: moduleLoadKindAmbient, Name: "foo", Entrypoint: false},
		{Kind: moduleLoadKindAmbient, Name: "bar-baz", Entrypoint: true},
		{Kind: moduleLoadKindAmbient, Name: "local", Entrypoint: true},
	}

	t.Run("constructor match loads only matching module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("unknown root field with multiple entrypoints loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"doThing"})
		require.Equal(t, mods, filtered)
	})

	t.Run("unknown root field with one entrypoint loads entrypoint", func(t *testing.T) {
		t.Parallel()

		oneEntrypoint := []pendingModule{mods[0], mods[1]}
		filtered := filterPendingWorkspaceModulesForRootFields(oneEntrypoint, nil, nil, []string{"doThing"})
		require.Equal(t, []pendingModule{mods[1]}, filtered)
	})

	t.Run("unknown root field skips entrypoint already recorded as failed", func(t *testing.T) {
		t.Parallel()

		oneEntrypoint := []pendingModule{mods[0], mods[1]}
		failed := map[string]error{"bar-baz": errors.New("boom")}
		filtered := filterPendingWorkspaceModulesForRootFields(oneEntrypoint, nil, failed, []string{"doThing"})
		require.Empty(t, filtered)
	})

	t.Run("named root field still selects a failed module", func(t *testing.T) {
		t.Parallel()

		failed := map[string]error{"foo": errors.New("boom")}
		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, failed, []string{"foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("introspection loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"__schema"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current typedefs loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"currentTypeDefs"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current module loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"currentModule"})
		require.Equal(t, mods, filtered)
	})

	t.Run("core-only query loads none", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"container", "version"})
		require.Empty(t, filtered)
	})

	t.Run("current workspace loads none (resolvers load on demand)", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"currentWorkspace"})
		require.Empty(t, filtered)
	})

	t.Run("already-served root field loads none", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		filtered := filterPendingWorkspaceModulesForRootFields(mods, served, nil, []string{"myMod"})
		require.Empty(t, filtered)
	})

	t.Run("served field combined with pending field loads only pending", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		filtered := filterPendingWorkspaceModulesForRootFields(mods, served, nil, []string{"myMod", "foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("env loads all (resolver snapshots served deps)", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"env"})
		require.Equal(t, mods, filtered)
	})

	t.Run("unrecognized loadFromID field loads all", func(t *testing.T) {
		t.Parallel()

		// The type name in load<Type>FromID needn't embed the module name, so
		// only a full load can guarantee the field exists.
		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, nil, []string{"loadSomethingFromID"})
		require.Equal(t, mods, filtered)
	})
}

func TestFilterPendingWorkspaceModulesForScopedRootFields(t *testing.T) {
	t.Parallel()

	foo := pendingModule{Kind: moduleLoadKindAmbient, Name: "foo"}
	barBaz := pendingModule{Kind: moduleLoadKindAmbient, Name: "barBaz"}
	entry := pendingModule{Kind: moduleLoadKindAmbient, Name: "entry", Entrypoint: true}
	mods := []pendingModule{foo, barBaz, entry}

	t.Run("no scope delegates to root-field demand", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs"}, "", false)
		require.False(t, applied)
		require.Equal(t, mods, selected)
	})

	t.Run("scoped typedefs loads target plus entrypoint", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs"}, "foo", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{foo, entry}, selected)
	})

	t.Run("kebab-case token matches declared module name", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs"}, "bar-baz", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{barBaz, entry}, selected)
	})

	t.Run("unknown token loads pending entrypoint alone", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs"}, "greet", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{entry}, selected)
	})

	t.Run("unknown token without entrypoint loads all", func(t *testing.T) {
		t.Parallel()

		noEntry := []pendingModule{foo, barBaz}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(noEntry, nil, nil, []string{"currentTypeDefs"}, "greet", false)
		require.True(t, applied)
		require.Equal(t, noEntry, selected)
	})

	t.Run("another full-schema field loads all without consuming", func(t *testing.T) {
		t.Parallel()

		for _, field := range []string{"env", "__schema", "currentModule"} {
			selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs", field}, "foo", false)
			require.False(t, applied, field)
			require.Equal(t, mods, selected, field)
		}
	})

	t.Run("no typedefs field delegates without consuming", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"foo"}, "barBaz", false)
		require.False(t, applied)
		require.Equal(t, []pendingModule{foo}, selected)
	})

	t.Run("typedefs plus module field unions both demands", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, nil, []string{"currentTypeDefs", "foo"}, "bar-baz", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{foo, barBaz, entry}, selected)
	})

	t.Run("served target contributes nothing to load", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, served, nil, []string{"currentTypeDefs"}, "myMod", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{entry}, selected)
	})

	t.Run("unknown token with served entrypoint loads no siblings", func(t *testing.T) {
		t.Parallel()

		// A prior selective request may have loaded the entrypoint without
		// consuming the scope. The later typedefs request is then already
		// satisfied by that served entrypoint and must not fall back to every
		// still-pending workspace module.
		served := map[string]struct{}{"entry": {}}
		pending := []pendingModule{foo, barBaz}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(pending, served, nil, []string{"currentTypeDefs"}, "greet", true)
		require.True(t, applied)
		require.Empty(t, selected)
	})
}

func TestEnsureRequestModulesLoadedConsumesScopeBeforeUnlock(t *testing.T) {
	client := &clientRuntime{clientRecord: &clientRecord{
		clientID: "client",
		clientMetadata: &engine.ClientMetadata{
			WorkspaceModuleScope: "good",
		}},
		pendingModules: []pendingModule{
			{Kind: moduleLoadKindAmbient, Name: "bad"},
		},
		servedWorkspaceModuleNames: map[string]struct{}{"good": {}}}
	req := httptest.NewRequest(http.MethodPost, engine.QueryEndpoint, strings.NewReader(`{"query":"{ currentTypeDefs { name } }"}`))
	req.Header.Set("Content-Type", "application/json")

	postLoad := make(chan struct{})
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)

	loadDone := make(chan error, 1)
	go func() {
		loadDone <- (&Server{}).ensureRequestModulesLoadedWithPostLoad(context.Background(), client, req, func() {
			close(postLoad)
			<-resume
		})
	}()

	select {
	case <-postLoad:
	case <-time.After(10 * time.Second):
		t.Fatal("module loading did not return")
	}

	client.modulesMu.Lock()
	observedScope := client.pendingWorkspaceModuleScopeLocked()
	client.modulesMu.Unlock()

	release()
	require.Empty(t, observedScope, "the one-shot scope was visible after the successful load released modulesMu")
	select {
	case err := <-loadDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("request module loading did not finish")
	}
}

func TestWithRequestTelemetrySuppression(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, engine.QueryEndpoint, nil)
	ctx, suppressed := withRequestTelemetrySuppression(context.Background(), req)
	require.False(t, suppressed)
	require.False(t, dagql.IsSkipped(ctx))

	req.Header.Set(engine.SuppressTelemetryHeader, "false")
	ctx, suppressed = withRequestTelemetrySuppression(context.Background(), req)
	require.False(t, suppressed)
	require.False(t, dagql.IsSkipped(ctx))

	req.Header.Set(engine.SuppressTelemetryHeader, "true")
	ctx, suppressed = withRequestTelemetrySuppression(context.Background(), req)
	require.True(t, suppressed)
	require.True(t, dagql.IsSkipped(ctx), "the suppressed request's context must carry the dagql skip flag so core.AroundFunc emits nothing")
}

// TestCallPayloadDeliveryStore pins the claim scoping that keeps call-payload
// telemetry per delivery target: a digest claimed by one client's emission
// must NOT count as seen for a client outside that emission's delivery domain
// (the AGENT_QA P0 — a nested `dagger agent` attaching to a long-running
// session could never rebuild worker IDs, because the session-wide claim was
// spent before its DB existed).
func TestCallPayloadDeliveryStore(t *testing.T) {
	t.Parallel()

	sess := &daggerSession{}
	storeA := &callPayloadDeliveryStore{session: sess, targets: []string{"clientA"}}
	require.True(t, storeA.CallPayloadNeedsEmission("xxh3:abc"))
	require.Equal(t, []string{"clientA"}, sess.callPayloadMissingTargets("xxh3:abc", storeA.targets, true))
	require.False(t, storeA.CallPayloadNeedsEmission("xxh3:abc"))

	storeB := &callPayloadDeliveryStore{session: sess, targets: []string{"clientB"}}
	require.True(t, storeB.CallPayloadNeedsEmission("xxh3:abc"),
		"a digest claimed by another client's emission must stay needed for a late client")

	storeMod := &callPayloadDeliveryStore{session: sess, targets: []string{"clientA", "modClient"}}
	require.True(t, storeMod.CallPayloadNeedsEmission("xxh3:abc"),
		"an emission must not be skipped while any route target still needs it")
	require.Equal(t, []string{"modClient"},
		sess.callPayloadMissingTargets("xxh3:abc", storeMod.targets, true),
		"only the missing target must be claimed")
	require.False(t, storeMod.CallPayloadNeedsEmission("xxh3:abc"))
}

func TestCallPayloadClaimsConcurrentOverlappingRoutes(t *testing.T) {
	t.Parallel()

	sess := &daggerSession{}
	routes := [][]string{{"parent", "childA"}, {"parent", "childB"}}
	start := make(chan struct{})
	results := make(chan []string, len(routes))
	var ready sync.WaitGroup
	ready.Add(len(routes))
	for _, route := range routes {
		go func() {
			ready.Done()
			<-start
			results <- sess.callPayloadMissingTargets("xxh3:overlap", route, true)
		}()
	}
	ready.Wait()
	close(start)

	counts := map[string]int{}
	for range routes {
		for _, target := range <-results {
			counts[target]++
		}
	}
	require.Equal(t, map[string]int{"parent": 1, "childA": 1, "childB": 1}, counts,
		"overlapping decisions must atomically assign each target exactly once")
	require.Empty(t, sess.callPayloadMissingTargets("xxh3:overlap", []string{"parent", "childA", "childB"}, true))
}

func TestSessionLogExporterRoutesCallPayloadOnlyToMissingTargets(t *testing.T) {
	t.Parallel()

	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	srv.telemetryPubSub = NewPubSub(srv)
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	parent := &clientRecord{daggerSession: sess, clientID: "parent"}
	child := &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}}
	sess.clientRecords[parent.clientID] = parent
	sess.clientRecords[child.clientID] = child
	exporter := sessionLogExporter{sess: sess, ps: srv.telemetryPubSub}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	logger := provider.Logger("test")

	emitPayload := func(origin string) {
		rec := otellog.Record{}
		rec.SetTimestamp(time.Now())
		rec.SetBody(otellog.StringValue(""))
		rec.AddAttributes(
			otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, origin),
			otellog.String(telemetryattrs.DagCallPayloadDigestAttr, "xxh3:partial"),
			otellog.String(telemetryattrs.DagCallPayloadAttr, "payload"),
		)
		logger.Emit(t.Context(), rec)
	}

	// Parent receives the payload first. Re-exporting it from the child route
	// must fill only the child gap instead of persisting a second parent row.
	emitPayload("parent")
	emitPayload("child")
	emitPayload("child")

	load := func(target string) []clientdb.Log {
		db, err := dbs.Open(t.Context(), target)
		require.NoError(t, err)
		defer db.Close()
		logs, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
		require.NoError(t, err)
		return logs
	}
	parentLogs := load("parent")
	childLogs := load("child")
	require.Len(t, parentLogs, 1)
	require.Len(t, childLogs, 1)

	for _, row := range append(parentLogs, childLogs...) {
		var attrs []*otlpcommonv1.KeyValue
		require.NoError(t, clientdb.UnmarshalProtoJSONs(row.Attributes, &otlpcommonv1.KeyValue{}, &attrs))
		var hasDigest, hasPayload bool
		for _, attr := range attrs {
			require.NotEqual(t, telemetryattrs.TelemetryOriginClientIDAttr, attr.Key,
				"routing-only origin must not be persisted or streamed")
			hasDigest = hasDigest || attr.Key == telemetryattrs.DagCallPayloadDigestAttr
			hasPayload = hasPayload || attr.Key == telemetryattrs.DagCallPayloadAttr
		}
		require.True(t, hasDigest, "routing must preserve the call payload wire representation")
		require.True(t, hasPayload, "routing must preserve the call payload wire representation")
	}
}

func TestFilterPendingWorkspaceModulesBySelectorInclude(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{Kind: moduleLoadKindAmbient, Name: "go-sdk"},
		{Kind: moduleLoadKindAmbient, Name: "rust-sdk"},
		{Kind: moduleLoadKindAmbient, Name: "php-sdk"},
	}

	t.Run("module:generator selects only that module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk:generate"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("module:item works for checks and services too", func(t *testing.T) {
		t.Parallel()

		// The module-name resolution is identical across generate/check/up: the
		// segment before ':' is the module name regardless of the item kind.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"rust-sdk:lint", "php-sdk:web"})
		require.Equal(t, []pendingModule{mods[1], mods[2]}, filtered)
	})

	t.Run("bare module name selects only that module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("multiple patterns select each named module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk", "php-sdk:api"})
		require.Equal(t, []pendingModule{mods[0], mods[2]}, filtered)
	})

	t.Run("bare token not matching a module selects all", func(t *testing.T) {
		t.Parallel()

		// e.g. an item served by the entrypoint module.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"generate"})
		require.Equal(t, mods, filtered)
	})

	t.Run("module:item not matching a module selects all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"typo-sdk:generate"})
		require.Equal(t, mods, filtered)
	})

	t.Run("empty include selects all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, nil)
		require.Equal(t, mods, filtered)
	})

	t.Run("already-served module is recognized and selects nothing", func(t *testing.T) {
		t.Parallel()

		// A re-evaluated selector (e.g. loading a GeneratorGroup from its ID
		// on a later request) names a module that already loaded; it must not
		// fall back to loading everything.
		served := map[string]struct{}{"dang-sdk": {}}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, served, []string{"dang-sdk"})
		require.Empty(t, filtered)
	})

	t.Run("served and pending patterns select only the pending module", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"dang-sdk": {}}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, served, []string{"dang-sdk:generate", "go-sdk"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("camelCase pattern selects the kebab-case module", func(t *testing.T) {
		t.Parallel()

		// Name matching is kebab-normalized on both sides, like the include
		// matchers the selector resolvers use (ModTreePath.Glob/CliCase).
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"goSdk:generate"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("kebab-case pattern selects the camelCase module", func(t *testing.T) {
		t.Parallel()

		// The CLI presents module commands in kebab-case, so a module declared
		// as "myMod" (or "mod1", which kebab-cases to "mod-1") is targeted by
		// its kebab-case name.
		camelMods := []pendingModule{
			{Kind: moduleLoadKindAmbient, Name: "myMod"},
			{Kind: moduleLoadKindAmbient, Name: "mod1"},
			{Kind: moduleLoadKindAmbient, Name: "other"},
		}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(camelMods, nil, []string{"my-mod", "mod-1:generate"})
		require.Equal(t, []pendingModule{camelMods[0], camelMods[1]}, filtered)
	})

	t.Run("glob pattern selects all", func(t *testing.T) {
		t.Parallel()

		// Glob metacharacters survive normalization and never equal a module
		// name, so glob patterns conservatively load everything.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-*"})
		require.Equal(t, mods, filtered)
	})
}

func TestWorkspaceConfigPendingModules(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{
		Root:       "/repo",
		Cwd:        ".",
		ConfigFile: workspace.ConfigFileName,
		LockFile:   filepath.Join(workspace.LockDirName, workspace.LockFileName),
	}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return filepath.Join("/resolved", relPath)
	}

	pending := workspaceConfigPendingModules(ws, &workspace.Config{
		DefaultsFromDotEnv: true,
		Modules: map[string]workspace.ModuleEntry{
			"zeta": {
				Source:     "github.com/acme/zeta@main",
				Entrypoint: true,
				Settings:   map[string]any{"message": "hello"},
			},
			"alpha": {
				Source:            "modules/alpha",
				LegacyDefaultPath: true,
			},
		},
	}, resolveLocalRef)
	require.Len(t, pending, 2)

	require.Equal(t, "alpha", pending[0].Name)
	require.Equal(t, "/resolved/modules/alpha", pending[0].Ref)
	require.Empty(t, pending[0].RefPin)
	require.False(t, pending[0].Entrypoint)
	require.True(t, pending[0].DisableFindUp)
	require.True(t, pending[0].LegacyDefaultPath)
	require.Equal(t, "/resolved", pending[0].DefaultPathContextSourceRef)
	require.True(t, pending[0].DefaultsFromDotEnv)

	require.Equal(t, "zeta", pending[1].Name)
	require.Equal(t, "github.com/acme/zeta@main", pending[1].Ref)
	require.Empty(t, pending[1].RefPin)
	require.True(t, pending[1].Entrypoint)
	require.True(t, pending[1].DisableFindUp)
	require.False(t, pending[1].LegacyDefaultPath)
	require.Empty(t, pending[1].DefaultPathContextSourceRef)
	require.True(t, pending[1].DefaultsFromDotEnv)
	require.Equal(t, map[string]any{"message": "hello"}, pending[1].ConfigDefaults)
}

// TestModuleResolutionFromSubdirectory verifies that module source paths from
// dagger.json are resolved relative to the config file location, not the
// client's working directory. When a client connects from sdk/go/, a module
// with source "modules/changelog" should resolve to /repo/modules/changelog,
// not /repo/sdk/go/modules/changelog.
func TestModuleResolutionFromSubdirectory(t *testing.T) {
	t.Parallel()

	// Filesystem layout:
	//   /repo/.git                  (git root)
	//   /repo/dagger.json           (config declaring a module)
	//   /repo/sdk/go/               (client CWD)

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	// The "toolchains" field is the current config mechanism for declaring
	// workspace modules in dagger.json.
	daggerJSON := `{
		"name": "myproject",
		"toolchains": [
			{"name": "changelog", "source": "modules/changelog"}
		]
	}`

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(daggerJSON), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		return filepath.Join(ws.Root, relPath)
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go", // CWD is a subdirectory
		resolveLocalRef,
		nil,
		true, // isLocal
	)
	require.NoError(t, err)
	require.Equal(t, "sdk/go", client.workspace.Cwd)

	// Module source must resolve relative to dagger.json (/repo),
	// not relative to CWD (/repo/sdk/go).
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, "/repo/modules/changelog", client.pendingModules[0].Ref)
	require.Equal(t, "changelog", client.pendingModules[0].Name)
}

func TestDetectAndLoadWorkspaceIgnoresCompatFallbackWhenConfigExists(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":                      true,
		"/repo/dagger.toml":               true,
		"/repo/mymod/dagger.json":         true,
		"/repo/modules/local":             true,
		"/repo/modules/local/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		switch filepath.Clean(path) {
		case "/repo/dagger.toml":
			return []byte(`[modules.dev]
source = "github.com/acme/dev@main"
entrypoint = true

[modules.local]
source = "modules/local"
`), nil
		case "/repo/mymod/dagger.json":
			return []byte(`{"name":"mymod","sdk":{"source":"go"}}`), nil
		default:
			return nil, os.ErrNotExist
		}
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, "mymod", client.workspace.Cwd)
	require.Equal(t, workspace.ConfigFileName, client.workspace.ConfigFile)

	require.Len(t, client.pendingModules, 2)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "dev", client.pendingModules[0].Name)
	require.Equal(t, "github.com/acme/dev@main", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)

	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[1].Kind)
	require.Equal(t, "local", client.pendingModules[1].Name)
	require.Equal(t, "/repo/modules/local", client.pendingModules[1].Ref)
	require.False(t, client.pendingModules[1].Entrypoint)
}

func TestDetectAndLoadWorkspaceLoadsPlainModuleCompatWithoutConfig(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":              true,
		"/repo/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/mymod/dagger.json" {
			return []byte(`{"name":"mymod","sdk":{"source":"go"}}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Empty(t, client.workspace.ConfigFile)
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "mymod", client.pendingModules[0].Name)
	require.Equal(t, "/repo/mymod", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceKeepsCompatFallbackForExplicitExtraModule(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(`{"name":"ambient","toolchains":[{"name":"tool","source":"./tool"}]}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	extra := []engine.ExtraModule{{
		Ref:        "/repo/explicit",
		Entrypoint: true,
	}}
	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			ExtraModules: extra,
		}},
		pendingWorkspaceLoad: true,
		pendingExtraModules:  extra}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.NotNil(t, client.workspace.CompatWorkspace())
	require.Empty(t, client.pendingModules)
	require.Equal(t, extra, client.pendingExtraModules)
}

func TestDetectAndLoadWorkspaceCreatesRootlessWorkspaceWithoutInferringModule(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/tmp/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/tmp/mymod/dagger.json" {
			return []byte(`{"name":"mymod"}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/tmp/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.Equal(t, "/tmp/mymod", client.workspace.HostPath())
	_, ok := client.workspace.Source().(*core.WorkspaceSourceRootlessLocal)
	require.True(t, ok)
	require.Empty(t, client.pendingModules)
}

func TestRemoteWorkspaceCwdUsesDetectionStart(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"dagger.toml": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "dagger.toml" {
			return []byte("# workspace\n"), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
		nil,
		nil,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, "subdir", client.workspace.Cwd)
	require.Equal(t, "github.com/acme/repo/subdir@main", client.workspace.Address)
	require.Equal(t, workspace.ConfigFileName, client.workspace.ConfigFile)
}

func TestRemoteWorkspaceLoadsPlainModuleCompatFromCWD(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"subdir/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "subdir/dagger.json" {
			return []byte(`{"name":"remote-mod","sdk":{"source":"go"}}`), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir/child",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
		nil,
		nil,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("subdir", "child"), client.workspace.Cwd)
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "remote-mod", client.pendingModules[0].Name)
	require.Equal(t, core.GitRefString("github.com/acme/repo", "subdir", "main"), client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceDoesNotLoadModulesByDefault(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(`{"name":"myproject","toolchains":[{"name":"changelog","source":"modules/changelog"}]}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &clientRuntime{clientRecord: &clientRecord{
		clientMetadata: &engine.ClientMetadata{}},
		pendingWorkspaceLoad: true}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.NotNil(t, client.workspace.CompatWorkspace())
	require.Empty(t, client.pendingModules)
}

func TestIsSameModuleReference(t *testing.T) {
	t.Parallel()

	local := func(contextPath, rootSubpath, sourceSubpath string) *core.ModuleSource {
		return &core.ModuleSource{
			Kind:              core.ModuleSourceKindLocal,
			Local:             &core.LocalModuleSource{ContextDirectoryPath: contextPath},
			SourceRootSubpath: rootSubpath,
			SourceSubpath:     sourceSubpath,
		}
	}

	t.Run("same local source root and pin", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod", ".", ".")
		b := local("/work/mod", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})

	t.Run("different local source", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod-a", ".", ".")
		b := local("/work/mod-b", ".", ".")
		require.False(t, isSameModuleReference(a, b))
	})

	t.Run("same module through different local refs", func(t *testing.T) {
		t.Parallel()
		// a points at the workspace root where dagger.json has sourceSubpath
		// ".dagger/modules/dagger-dev". b points directly at that module dir.
		a := local("/root/src/dagger", ".", ".dagger/modules/dagger-dev")
		b := local("/root/src/dagger/.dagger/modules/dagger-dev", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})
}

func TestEnsureWorkspaceLoadedInheritsParentWorkspace(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	bound := &core.Workspace{
		ClientID: "parent-client",
	}

	parent := &clientRuntime{clientRecord: &clientRecord{
		clientID: "parent-client"},
		workspace: bound}
	child := &clientRuntime{clientRecord: &clientRecord{
		clientID:        "child-client",
		parentClientIDs: []string{parent.clientID}}}
	sess := &daggerSession{clientRuntimes: map[string]*clientRuntime{
		parent.clientID: parent,
		child.clientID:  child,
	}}
	parent.daggerSession = sess
	child.daggerSession = sess
	installTestClientRecords(sess)

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, bound, child.workspace)
}

func TestEnsureWorkspaceLoadedKeepsExistingWorkspaceBinding(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	existing := &core.Workspace{
		ClientID: "child-client",
	}
	parentBound := &core.Workspace{
		ClientID: "parent-client",
	}

	parent := &clientRuntime{clientRecord: &clientRecord{
		clientID: "parent-client"},
		workspace: parentBound}
	child := &clientRuntime{clientRecord: &clientRecord{
		clientID:        "child-client",
		parentClientIDs: []string{parent.clientID}},
		workspace: existing}

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, existing, child.workspace)
}

func TestSpecificClientAttachableConnFallsBackForSyntheticClient(t *testing.T) {
	t.Parallel()

	// Builtin Dang agent tools execute through a synthetic nested client with no
	// attachable connection of its own. Secret and socket values bind that client
	// as their source, so specific-client access must follow its explicit proxy
	// instead of waiting forever for an attachable that will never be registered.
	parentConn := &grpc.ClientConn{}
	parentCaller := &sessionAttachableCaller{
		ctx:       context.Background(),
		conn:      parentConn,
		supported: map[string]struct{}{},
	}
	parent := &clientRecord{clientID: "parent"}
	child := &clientRecord{
		clientID:                 "child",
		hostServiceProxyClientID: parent.clientID,
		parentClientIDs:          []string{parent.clientID},
	}
	attachables := newSessionAttachableManager()
	attachables.callers[parent.clientID] = parentCaller
	sess := &daggerSession{
		sessionID:   "session",
		attachables: attachables,
		clientRecords: map[string]*clientRecord{
			parent.clientID: parent,
			child.clientID:  child,
		},
	}
	parent.daggerSession = sess
	child.daggerSession = sess
	sess.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{sess.sessionID: sess}}

	for _, ifAvailable := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			SessionID: sess.sessionID,
			ClientID:  child.clientID,
		})
		conn, ok, err := srv.SpecificClientAttachableConn(ctx, child.clientID, core.SpecificClientAttachableConnOpts{
			IfAvailable: ifAvailable,
		})
		cancel()
		require.NoError(t, err)
		require.True(t, ok)
		require.Same(t, parentConn, conn)
	}
}

func TestResolveHostServiceCallerFallsBackToParentRecordWithoutRuntime(t *testing.T) {
	t.Parallel()

	parentCaller := &fakeSessionCaller{id: "parent"}
	parent := &clientRecord{clientID: "parent"}
	child := &clientRecord{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parentClientIDs:          []string{parent.clientID},
	}
	sess := &daggerSession{
		attachables: newSessionAttachableManager(),
		clientRecords: map[string]*clientRecord{
			parent.clientID: parent,
			child.clientID:  child,
		},
		clientRuntimes: map[string]*clientRuntime{},
	}
	parent.daggerSession = sess
	child.daggerSession = sess
	sess.getClientCaller = func(_ context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "parent", id)
		return parentCaller, nil
	}

	caller, err := sess.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, parentCaller, caller)
	require.Empty(t, sess.clientRuntimes, "host proxy routing must use records, not retained runtimes")
}

func TestResolveHostServiceCallerPrefersCurrentClientAttachable(t *testing.T) {
	t.Parallel()

	currentCaller := &sessionAttachableCaller{
		ctx:       context.Background(),
		supported: map[string]struct{}{},
	}
	attachables := newSessionAttachableManager()
	attachables.callers["child"] = currentCaller

	parent := &clientRecord{clientID: "parent"}
	child := &clientRecord{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parentClientIDs:          []string{parent.clientID},
	}
	sess := &daggerSession{
		attachables: attachables,
		clientRecords: map[string]*clientRecord{
			parent.clientID: parent,
			child.clientID:  child,
		},
	}
	sess.getClientCaller = func(context.Context, string) (engineutil.SessionCaller, error) {
		t.Fatal("unexpected parent fallback")
		return nil, nil
	}

	caller, err := sess.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, currentCaller, caller)
}

func TestResolveHostServiceCallerUsesBlockingLookupForOtherClients(t *testing.T) {
	t.Parallel()

	otherCaller := &fakeSessionCaller{id: "other"}
	sess := &daggerSession{}
	sess.getClientCaller = func(_ context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "other", id)
		return otherCaller, nil
	}

	caller, err := sess.resolveHostServiceCaller(context.Background(), "other")
	require.NoError(t, err)
	require.Same(t, otherCaller, caller)
}

func TestWorkspaceBindingMode(t *testing.T) {
	t.Parallel()

	t.Run("declared workspace takes precedence", func(t *testing.T) {
		t.Parallel()

		client := &clientRuntime{clientRecord: &clientRecord{
			clientMetadata: &engine.ClientMetadata{
				Workspace: stringPtr("github.com/dagger/dagger@main"),
			}},
			pendingWorkspaceLoad: false}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDeclared, mode)
		require.Equal(t, "github.com/dagger/dagger@main", workspaceRef)
	})

	t.Run("non-module defaults to host detection", func(t *testing.T) {
		t.Parallel()

		client := &clientRuntime{clientRecord: &clientRecord{
			clientMetadata: &engine.ClientMetadata{}},
			pendingWorkspaceLoad: true}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDetectHost, mode)
		require.Equal(t, "", workspaceRef)
	})

	t.Run("module defaults to inheritance", func(t *testing.T) {
		t.Parallel()

		client := &clientRuntime{clientRecord: &clientRecord{
			clientMetadata: &engine.ClientMetadata{}},
			pendingWorkspaceLoad: false}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingInherit, mode)
		require.Equal(t, "", workspaceRef)
	})
}

func TestBuildCoreWorkspaceIncludesConfigState(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "main-client",
	})

	t.Run("workspace with config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:       "/repo",
			HasGitRoot: true,
			Cwd:        filepath.Join("services", "payment", "src"),
			ConfigFile: filepath.Join("services", "payment", workspace.ConfigFileName),
			LockFile:   filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Equal(t, "file:///repo/services/payment/src", ws.Address)
		require.Equal(t, filepath.Join("services", "payment", "src"), ws.Cwd)
		require.Equal(t, filepath.Join("services", "payment", workspace.ConfigFileName), ws.ConfigFile)
		require.Equal(t, filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName), ws.LockFile)
		require.Equal(t, "/repo", ws.HostPath())
	})

	t.Run("workspace without config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:       "/repo",
			HasGitRoot: true,
			Cwd:        ".",
			LockFile:   filepath.Join(workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Empty(t, ws.ConfigFile)
		require.Equal(t, filepath.Join(workspace.LockDirName, workspace.LockFileName), ws.LockFile)
	})

	t.Run("local boundary without Git is rootless", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:     "/repo",
			Cwd:      ".",
			LockFile: workspace.LockFileName,
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Equal(t, "/repo", ws.HostPath())
		_, ok := ws.Source().(*core.WorkspaceSourceRootlessLocal)
		require.True(t, ok)
		_, err = ws.ExportHostPath()
		require.ErrorContains(t, err, "requires a local Git workspace")
	})
}

func TestNestedClientMetadataForRequest(t *testing.T) {
	t.Parallel()

	baseMetadata := func() *engine.ClientMetadata {
		return &engine.ClientMetadata{
			ClientID:          "nested-client",
			ClientSecretToken: "secret",
			SessionID:         "session",
			ClientHostname:    "nested-host",
			ClientStableID:    "stable",
			ClientVersion:     "",
			Labels: map[string]string{
				"ignored": "true",
			},
			SSHAuthSocketPath: "/tmp/ssh.sock",
			AllowedLLMModules: []string{"parent"},
			ExtraModules: []engine.ExtraModule{{
				Ref: "github.com/dagger/base-extra",
			}},
			LoadWorkspaceModules:  true,
			EagerRuntime:          true,
			Workspace:             stringPtr("github.com/dagger/base@main"),
			WorkspaceEnv:          stringPtr("parent-ci"),
			WorkspaceModuleScope:  "parent-scope",
			UseRecipeIDsByDefault: true,
		}
	}

	t.Run("inherits live nested client identity and policy without forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		md := nestedClientMetadataForRequest(http.Header{}, base)

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, engine.Version, md.ClientVersion)
		require.Empty(t, md.Labels)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
		require.Empty(t, md.ExtraModules)
		require.False(t, md.LoadWorkspaceModules)
		require.False(t, md.EagerRuntime)
		require.Nil(t, md.Workspace)
		require.Nil(t, md.WorkspaceEnv)
		require.Empty(t, md.WorkspaceModuleScope)
		require.True(t, md.UseRecipeIDsByDefault)

		base.AllowedLLMModules[0] = "mutated"
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
	})

	t.Run("overlays request-scoped forwarded metadata", func(t *testing.T) {
		t.Parallel()

		workspaceRef := "github.com/dagger/dagger@main"
		workspaceEnv := "ci"
		forwarded := engine.ClientMetadata{
			ClientID:          "forwarded-client",
			ClientSecretToken: "forwarded-secret",
			SessionID:         "forwarded-session",
			ClientHostname:    "forwarded-host",
			ClientStableID:    "forwarded-stable",
			ClientVersion:     "v-test",
			Labels: map[string]string{
				"forwarded": "ignored",
			},
			SSHAuthSocketPath: "/tmp/forwarded-ssh.sock",
			AllowedLLMModules: []string{"child"},
			ExtraModules: []engine.ExtraModule{{
				Ref:        "github.com/dagger/mod",
				Entrypoint: true,
			}},
			LoadWorkspaceModules:           true,
			EagerRuntime:                   true,
			SuppressCompatWorkspaceWarning: true,
			Workspace:                      &workspaceRef,
			WorkspaceEnv:                   &workspaceEnv,
			WorkspaceModuleScope:           "good-mod",
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), baseMetadata())

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Empty(t, md.Labels)

		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.True(t, md.LoadWorkspaceModules)
		require.True(t, md.EagerRuntime)
		require.True(t, md.SuppressCompatWorkspaceWarning)
		require.Equal(t, "github.com/dagger/dagger@main", *md.Workspace)
		require.Equal(t, "ci", *md.WorkspaceEnv)
		require.Equal(t, "good-mod", md.WorkspaceModuleScope)
		require.Equal(t, []engine.ExtraModule{{
			Ref:        "github.com/dagger/mod",
			Entrypoint: true,
		}}, md.ExtraModules)
		require.True(t, md.UseRecipeIDsByDefault)
	})

	t.Run("does not accept internal recipe ID default from forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		base.UseRecipeIDsByDefault = false
		forwarded := engine.ClientMetadata{
			ClientVersion:         "v-test",
			UseRecipeIDsByDefault: true,
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), base)

		require.False(t, md.UseRecipeIDsByDefault)
	})
}

func TestLocalWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "file:///repo", localWorkspaceAddress("/repo", "."))
	require.Equal(t, "file:///repo/services/payment", localWorkspaceAddress("/repo", "services/payment"))
}

func TestRemoteWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://github.com/dagger/dagger@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", ".", "main"))
	require.Equal(t, "https://github.com/dagger/dagger/services/payment@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", "services/payment", "main"))
}

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})
}

func TestGatherModuleLoadRequests(t *testing.T) {
	t.Parallel()

	loads := gatherModuleLoadRequests(
		[]pendingModule{
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/a", Name: "a"},
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/b", Name: "b"},
		},
		[]engine.ExtraModule{
			{Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true},
			{Ref: "github.com/acme/extra2", Name: "extra2"},
		},
	)

	require.Len(t, loads, 4)
	require.Equal(t, moduleLoadKindAmbient, loads[0].mod.Kind)
	require.Equal(t, moduleLoadKindAmbient, loads[1].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[2].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[3].mod.Kind)

	require.Equal(t, "github.com/acme/a", loads[0].mod.Ref)
	require.Equal(t, "github.com/acme/b", loads[1].mod.Ref)
	require.Equal(t, "github.com/acme/extra1", loads[2].mod.Ref)
	require.Equal(t, "github.com/acme/extra2", loads[3].mod.Ref)
	require.True(t, loads[2].mod.Entrypoint)
}

func TestModuleLoadParallelism(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, moduleLoadParallelism(0))
	require.Equal(t, 1, moduleLoadParallelism(1))
	require.Equal(t, 3, moduleLoadParallelism(3))
	require.Equal(t, maxParallelModuleResolves, moduleLoadParallelism(maxParallelModuleResolves+4))
}

func TestModuleLoadErr(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")

	normal := moduleLoadErr(moduleLoadRequest{mod: pendingModule{Ref: "github.com/acme/mod"}}, err)
	require.ErrorContains(t, normal, `loading module "github.com/acme/mod": boom`)

	extra := moduleLoadErr(moduleLoadRequest{
		mod: pendingModule{
			Kind: moduleLoadKindExtra,
			Ref:  "github.com/acme/extra",
		},
	}, err)
	require.ErrorContains(t, extra, `loading extra module "github.com/acme/extra": boom`)
}

func TestDedupeResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	loads := []moduleLoadRequest{
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: false,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindExtra,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: true,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/other",
				Name:       "other",
				Entrypoint: false,
			},
		},
	}
	resolved := []resolvedModuleLoad{
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: false},
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
		{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: false},
	}

	dedupLoads, dedupResolved := dedupeResolvedModuleLoads(loads, resolved)
	require.Len(t, dedupLoads, 2)

	require.Equal(t, moduleLoadKindExtra, dedupLoads[0].mod.Kind)
	require.True(t, dedupResolved[0].primaryEntrypoint)

	require.Equal(t, moduleLoadKindAmbient, dedupLoads[1].mod.Kind)
	require.False(t, dedupResolved[1].primaryEntrypoint)
}

func TestArbitrateResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	t.Run("extra beats ambient", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra", Name: "extra", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.NoError(t, err)
		require.False(t, resolved[0].primaryEntrypoint)
		require.True(t, resolved[1].primaryEntrypoint)
	})

	t.Run("multiple ambient entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/other", Name: "other", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid workspace configuration: multiple distinct ambient entrypoint modules: app, other")
	})

	t.Run("multiple extra entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra2", Name: "extra2", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "extra1"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra2"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid extra-module request: multiple distinct extra-module entrypoints: extra1, extra2")
	})
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}

func TestReadWorkspaceLockStateReadsLegacyLockFallback(t *testing.T) {
	t.Parallel()

	legacy := workspace.NewLock()
	require.NoError(t, legacy.SetLookup("", "oci-sha", []any{"alpine:latest"}, "sha256:deadbeef"))
	legacyBytes, err := legacy.Marshal()
	require.NoError(t, err)

	ws := &core.Workspace{
		ConfigFile: "dagger.toml",
		LockFile:   "dagger.lock",
	}
	ws.SetHostPath("/repo")

	lock, err := readWorkspaceLockState(t.Context(), fakeWorkspaceLockStateReader{
		files: map[string][]byte{
			filepath.Join("/repo", ".dagger", "lock"): legacyBytes,
		},
	}, ws)
	require.NoError(t, err)

	got, ok := lock.GetLookup("", "oci-sha", []any{"alpine:latest"})
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", got)
}

type fakeWorkspaceLockStateReader struct {
	files map[string][]byte
}

func (r fakeWorkspaceLockStateReader) ReadCallerHostFile(_ context.Context, path string) ([]byte, error) {
	if data, ok := r.files[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func stringPtr(v string) *string {
	return &v
}

func sessionTestModuleResult(t *testing.T, name string) dagql.ObjectResult[*core.Module] {
	t.Helper()

	dag, err := dagql.NewServer(t.Context(), &core.Module{})
	require.NoError(t, err)
	res, err := dagql.NewObjectResultForCall(
		&core.Module{NameField: name},
		dag,
		&dagql.ResultCall{SyntheticOp: "session-test-module-" + name},
	)
	require.NoError(t, err)
	return res
}
