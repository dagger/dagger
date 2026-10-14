package server

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// sharedWorkTestSession is an initialized session with one client and a held
// request scope, built like the client lifecycle tests' sessions, so leases
// are cloned and refused by the session's real lease bookkeeping.
func sharedWorkTestSession(t *testing.T, id string) (sess *daggerSession, request, transport engine.ClientScope) {
	t.Helper()
	client := &clientRuntime{clientRecord: &clientRecord{
		clientID:       id + "-client",
		clientMetadata: &engine.ClientMetadata{SessionID: id, ClientID: id + "-client"},
		metadataSealed: true,
		accepting:      true},
		state:           clientStateInitialized,
		lifecycleLeases: make(map[uint64]clientLifecycleLeaseRecord)}
	sess = &daggerSession{
		sessionID:      id,
		clientRuntimes: map[string]*clientRuntime{client.clientID: client},
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.state.Store(sessionStateInitialized)
	transport, err := sess.acquireRootClientScope(client, engine.ClientLeaseTransport, "proxy")
	require.NoError(t, err)
	client.transportLease = transport.Lease()
	t.Cleanup(transport.Lease().Release)
	request, err = sess.acquireRootClientScope(client, engine.ClientLeaseRequest, "POST /query")
	require.NoError(t, err)
	t.Cleanup(request.Lease().Release)
	return sess, request, transport
}

// awaitCallWaiters waits until the cache reports the ongoing call with
// concurrencyKey having want waiters. The debug snapshot reads the ongoing
// calls under the cache's own lock.
func awaitCallWaiters(t *testing.T, cache *dagql.Cache, concurrencyKey string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var buf bytes.Buffer
		require.NoError(t, cache.WriteDebugCacheSnapshot(&buf))
		var snapshot struct {
			OngoingCalls []dagql.CacheDebugOngoingCall `json:"ongoing_calls"`
		}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &snapshot))
		for _, call := range snapshot.OngoingCalls {
			if call.ConcurrencyKey == concurrencyKey && call.Waiters == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ongoing call %q never had %d waiters: %+v", concurrencyKey, want, snapshot.OngoingCalls)
		}
		time.Sleep(time.Millisecond)
	}
}

// receiveWithin returns the next value from ch, failing the test if none
// arrives within ten seconds.
func receiveWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// A call two sessions share keeps working for the session still waiting on it
// when the session that started it is marked removed. The shared work runs
// under the producer's client scope; session removal marks the session removed
// before it waits for that scope's leases to drain (removeDaggerSession), so
// the shared work is still running then. A nested call it makes must not fail
// the surviving waiter with the producer's closure.
//
// The three events are ordered: the waiter has joined the shared call; the
// producer's session is marked removed, as removeDaggerSession does first; the
// shared work then makes its nested call. This reproduces the early teardown
// window, before the cache releases the producer's session; it does not run
// the rest of session teardown.
func TestSharedCallSurvivesItsProducersSessionRemoval(t *testing.T) {
	t.Parallel()
	producer, producerScope, producerTransport := sharedWorkTestSession(t, "producer")
	_, waiterScope, _ := sharedWorkTestSession(t, "waiter")

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, cache.Close(ctx))
	})
	dag, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)

	scoped := func(ctx context.Context, scope engine.ClientScope) context.Context {
		ctx, err := engine.ContextWithClientScope(dagql.ContextWithCache(ctx, cache), scope)
		require.NoError(t, err)
		return ctx
	}
	producerCtx, cancelProducer := context.WithCancel(scoped(t.Context(), producerScope))
	defer cancelProducer()
	waiterCtx := scoped(t.Context(), waiterScope)

	const sharedKey = "shared-work"
	shared := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "sharedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}
	nested := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "nestedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}

	started, proceed := make(chan struct{}), make(chan struct{})
	releaseWork := sync.OnceFunc(func() { close(proceed) })
	var signalStarted sync.Once
	// Both sessions' copies of the call do the same work, so a surviving
	// waiter that takes the work over gets the same value.
	work := func(ctx context.Context) (dagql.AnyResult, error) {
		signalStarted.Do(func() { close(started) })
		<-proceed
		// The shared work's own nested call, under the shared work's context.
		md, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := cache.GetOrInitCall(ctx, md.SessionID, dag, &dagql.CallRequest{ResultCall: nested, ConcurrencyKey: "nested-work"}, func(context.Context) (dagql.AnyResult, error) {
			return dagql.NewResultForCall(dagql.String("nested"), nested)
		}); err != nil {
			return nil, err
		}
		return dagql.NewResultForCall(dagql.String("shared"), shared)
	}

	type outcome struct {
		res dagql.AnyResult
		err error
	}
	producerDone := make(chan error, 1)
	waiterDone := make(chan outcome, 1)
	var calls sync.WaitGroup
	// Runs before the cache closes: no call is left blocked on the work.
	t.Cleanup(func() {
		releaseWork()
		cancelProducer()
		calls.Wait()
	})

	calls.Add(1)
	go func() {
		defer calls.Done()
		_, err := cache.GetOrInitCall(producerCtx, producer.sessionID, dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
		producerDone <- err
	}()
	receiveWithin(t, started, "the shared work to start")

	calls.Add(1)
	go func() {
		defer calls.Done()
		res, err := cache.GetOrInitCall(waiterCtx, "waiter", dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
		waiterDone <- outcome{res, err}
	}()
	// 1. The waiter has joined the shared call.
	awaitCallWaiters(t, cache, sharedKey, 2)

	// 2. The producer's session is marked removed, as removeDaggerSession
	// begins: the removed state is published and scope teardown starts before
	// the session waits for its leases to drain. Its own request then goes away.
	producer.markSessionRemoved()
	producer.beginClientScopeTeardown()
	cancelProducer()
	require.ErrorIs(t, receiveWithin(t, producerDone, "the producer's request to end"), context.Canceled)
	drained := make(chan error, 1)
	go func() { drained <- producer.waitForClientScopeDrain(t.Context()) }()
	producerScope.Lease().Release()
	producerTransport.Lease().Release()

	// 3. The shared work, still running under the producer's scope, makes its
	// nested call.
	releaseWork()
	got := receiveWithin(t, waiterDone, "the waiter's call to return")
	require.NoError(t, got.err, "the waiter's session is alive; the shared call it joined must not fail with another session's closure")
	require.Equal(t, dagql.String("shared"), got.res.Unwrap())
	require.NoError(t, receiveWithin(t, drained, "the producer's scopes to drain"))
}
