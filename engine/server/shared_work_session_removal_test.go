package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/analytics"
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

// sharedWorkTeardownSession gives a sharedWorkTestSession the state that
// removeDaggerSession tears down, built like the session teardown tests'
// sessions.
func sharedWorkTeardownSession(t *testing.T, id string) (sess *daggerSession, request engine.ClientScope) {
	t.Helper()
	sess, request, _ = sharedWorkTestSession(t, id)
	sess.mainClientCallerID = id + "-client"
	sess.services = core.NewServices()
	sess.analytics = analytics.New(analytics.Config{DoNotTrack: true})
	sess.shutdownCh = make(chan struct{})
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	return sess, request
}

// awaitScopeDrainWait waits until session teardown is parked waiting for the
// session's client scopes to drain: every earlier teardown step, including the
// cache's release of the session, has run.
func awaitScopeDrainWait(t *testing.T, sess *daggerSession) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		sess.scopeMu.Lock()
		parked := sess.scopeWaitCh != nil
		sess.scopeMu.Unlock()
		if parked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("session teardown never waited for its client scopes to drain")
		}
		time.Sleep(time.Millisecond)
	}
}

// The same shared call as TestSharedCallSurvivesItsProducersSessionRemoval,
// through the rest of session teardown: the shared work makes its nested call
// only once removeDaggerSession has stopped the producer's services, closed
// its resolver and released its cache session, and is waiting for the shared
// work's scope to drain. The surviving waiter must still get the call's value,
// and the producer's teardown must finish once the work is done.
func TestSharedCallSurvivesItsProducersFullSessionTeardown(t *testing.T) {
	t.Parallel()
	srv := newTeardownTestServer(t)
	cache := srv.engineCache
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, cache.Close(ctx))
	})
	producer, producerScope := sharedWorkTeardownSession(t, "producer")
	_, waiterScope, _ := sharedWorkTestSession(t, "waiter")
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
	var nestedMu sync.Mutex
	var nestedSessions, workers []string
	// Each session's callback is its own, as an engine resolver closes over its
	// own client. A failure reaches the waiters as whatever error the work
	// returns: here it does not wrap the cause, as a torn-down dependency's
	// error would not.
	workFor := func(session string) func(context.Context) (dagql.AnyResult, error) {
		return func(ctx context.Context) (dagql.AnyResult, error) {
			signalStarted.Do(func() { close(started) })
			<-proceed
			nestedMu.Lock()
			workers = append(workers, session)
			nestedMu.Unlock()
			md, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return nil, err
			}
			if _, err := cache.GetOrInitCall(ctx, md.SessionID, dag, &dagql.CallRequest{ResultCall: nested, ConcurrencyKey: "nested-work"}, func(context.Context) (dagql.AnyResult, error) {
				nestedMu.Lock()
				nestedSessions = append(nestedSessions, md.SessionID)
				nestedMu.Unlock()
				return dagql.NewResultForCall(dagql.String("nested"), nested)
			}); err != nil {
				return nil, errors.New(session + "'s dependency failed: " + err.Error())
			}
			return dagql.NewResultForCall(dagql.String("shared"), shared)
		}
	}

	type outcome struct {
		res dagql.AnyResult
		err error
	}
	producerDone := make(chan error, 1)
	waiterDone := make(chan outcome, 1)
	var calls sync.WaitGroup
	t.Cleanup(func() {
		releaseWork()
		cancelProducer()
		calls.Wait()
	})

	calls.Add(1)
	go func() {
		defer calls.Done()
		_, err := cache.GetOrInitCall(producerCtx, producer.sessionID, dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, workFor("producer"))
		producerDone <- err
	}()
	receiveWithin(t, started, "the shared work to start")

	calls.Add(1)
	go func() {
		defer calls.Done()
		res, err := cache.GetOrInitCall(waiterCtx, "waiter", dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, workFor("waiter"))
		waiterDone <- outcome{res, err}
	}()
	// 1. The waiter has joined the shared call.
	awaitCallWaiters(t, cache, sharedKey, 2)

	// 2. The producer's session is removed. Its request goes away, and teardown
	// runs until it waits for the shared work's scope to drain.
	removed := make(chan error, 1)
	calls.Add(1)
	go func() {
		defer calls.Done()
		removed <- srv.removeDaggerSession(t.Context(), producer)
	}()
	cancelProducer()
	require.ErrorIs(t, receiveWithin(t, producerDone, "the producer's request to end"), context.Canceled)
	producerScope.Lease().Release()
	awaitScopeDrainWait(t, producer)

	// 3. The shared work, still running under the producer's scope, makes its
	// nested call.
	releaseWork()
	got := receiveWithin(t, waiterDone, "the waiter's call to return")
	require.NoError(t, got.err, "the waiter's session is alive; the shared call it joined must not fail with another session's teardown")
	require.Equal(t, dagql.String("shared"), got.res.Unwrap())
	nestedMu.Lock()
	require.Equal(t, []string{"producer", "waiter"}, workers, "the surviving waiter took the work over with its own callback")
	require.Equal(t, []string{"waiter"}, nestedSessions, "the surviving waiter ran the work under its own session")
	nestedMu.Unlock()
	require.NoError(t, receiveWithin(t, removed, "the producer's session removal to finish"))
}

// lazySharedWorkValue is a value whose lazy evaluation the cache shares
// between every session that evaluates it.
type lazySharedWorkValue struct {
	dagql.String
	eval dagql.LazyEvalFunc
}

func (v *lazySharedWorkValue) LazyEvalFunc() dagql.LazyEvalFunc { return v.eval }

// A lazy evaluation two sessions share keeps working for the session still
// waiting on it when the session that started it is removed. The cache runs
// one evaluation under a clone of the first evaluator's (the producer's)
// client scope and every other evaluator waits on it. The producer's session
// is marked removed while the evaluation runs; the evaluation's nested call
// must not fail the surviving waiter with the producer's closure.
func TestSharedLazyEvaluationSurvivesItsProducersSessionRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		lazyCall := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "lazyWork", Type: dagql.NewResultCallType(dagql.String("").Type())}
		nested := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "nestedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}

		started, proceed := make(chan struct{}), make(chan struct{})
		releaseWork := sync.OnceFunc(func() { close(proceed) })
		var signalStarted sync.Once
		var nestedMu sync.Mutex
		var nestedSessions []string
		// The evaluation's own nested call, under the evaluation's context.
		eval := func(ctx context.Context) error {
			signalStarted.Do(func() { close(started) })
			<-proceed
			md, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return err
			}
			_, err = cache.GetOrInitCall(ctx, md.SessionID, dag, &dagql.CallRequest{ResultCall: nested, ConcurrencyKey: "nested-work"}, func(context.Context) (dagql.AnyResult, error) {
				nestedMu.Lock()
				nestedSessions = append(nestedSessions, md.SessionID)
				nestedMu.Unlock()
				return dagql.NewResultForCall(dagql.String("nested"), nested)
			})
			return err
		}
		produce := func(context.Context) (dagql.AnyResult, error) {
			return dagql.NewResultForCall(&lazySharedWorkValue{String: "lazy", eval: eval}, lazyCall)
		}
		producerRes, err := cache.GetOrInitCall(producerCtx, producer.sessionID, dag, &dagql.CallRequest{ResultCall: lazyCall}, produce)
		require.NoError(t, err)
		waiterRes, err := cache.GetOrInitCall(waiterCtx, "waiter", dag, &dagql.CallRequest{ResultCall: lazyCall}, produce)
		require.NoError(t, err)

		producerDone := make(chan error, 1)
		waiterDone := make(chan error, 1)
		var evals sync.WaitGroup
		t.Cleanup(func() {
			releaseWork()
			cancelProducer()
			evals.Wait()
		})

		evals.Add(1)
		go func() {
			defer evals.Done()
			producerDone <- cache.Evaluate(producerCtx, producerRes)
		}()
		receiveWithin(t, started, "the lazy evaluation to start")

		evals.Add(1)
		go func() {
			defer evals.Done()
			waiterDone <- cache.Evaluate(waiterCtx, waiterRes)
		}()
		// 1. The waiter has joined the evaluation: every goroutine in the
		// bubble is blocked, the evaluation on its release and both evaluators
		// on its completion.
		synctest.Wait()

		// 2. The producer's session is marked removed, as removeDaggerSession
		// begins, and its own request goes away.
		producer.markSessionRemoved()
		producer.beginClientScopeTeardown()
		cancelProducer()
		require.ErrorIs(t, receiveWithin(t, producerDone, "the producer's evaluation to end"), context.Canceled)
		drained := make(chan error, 1)
		go func() { drained <- producer.waitForClientScopeDrain(t.Context()) }()
		producerScope.Lease().Release()
		producerTransport.Lease().Release()

		// 3. The evaluation, still running under the producer's scope, makes its
		// nested call.
		releaseWork()
		require.NoError(t, receiveWithin(t, waiterDone, "the waiter's evaluation to return"),
			"the waiter's session is alive; the evaluation it joined must not fail with another session's closure")
		nestedMu.Lock()
		require.Equal(t, []string{"waiter"}, nestedSessions, "the surviving waiter ran the evaluation under its own session")
		nestedMu.Unlock()
		require.NoError(t, receiveWithin(t, drained, "the producer's scopes to drain"))
	})
}

// attachingSharedWorkValue is a value whose publication attaches a dependency
// it obtains with a nested call, as module objects load their fields' results.
type attachingSharedWorkValue struct {
	dagql.String
	attach func(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error)
}

func (v *attachingSharedWorkValue) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return v.attach(ctx, attach)
}

// A shared call whose work succeeds, but whose result's publication fails
// because the producer's session is removed while it runs, still gives the
// surviving waiter the call's value. Publication runs under the shared work's
// context, the producer's scope. A third session that found the result while
// its publication was still open, and waits on it, must not get the failure
// either.
func TestSharedCallPublicationSurvivesItsProducersSessionRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer, producerScope, producerTransport := sharedWorkTestSession(t, "producer")
		_, waiterScope, _ := sharedWorkTestSession(t, "waiter")
		_, readerScope, _ := sharedWorkTestSession(t, "reader")

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
		readerCtx := scoped(t.Context(), readerScope)

		const sharedKey = "shared-work"
		shared := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "sharedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}
		nested := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "nestedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}

		workStarted, proceedWork := make(chan struct{}), make(chan struct{})
		attachStarted, proceedAttach := make(chan struct{}), make(chan struct{})
		releaseWork := sync.OnceFunc(func() { close(proceedWork) })
		releaseAttach := sync.OnceFunc(func() { close(proceedAttach) })
		var signalWork, signalAttach sync.Once
		var nestedMu sync.Mutex
		var nestedSessions []string
		// The publication's own nested call, under the publication's context.
		attachNested := func(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
			signalAttach.Do(func() { close(attachStarted) })
			<-proceedAttach
			md, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return nil, err
			}
			dep, err := cache.GetOrInitCall(ctx, md.SessionID, dag, &dagql.CallRequest{ResultCall: nested, ConcurrencyKey: "nested-work"}, func(context.Context) (dagql.AnyResult, error) {
				nestedMu.Lock()
				nestedSessions = append(nestedSessions, md.SessionID)
				nestedMu.Unlock()
				return dagql.NewResultForCall(dagql.String("nested"), nested)
			})
			if err != nil {
				return nil, err
			}
			attached, err := attach(dep)
			if err != nil {
				return nil, err
			}
			return []dagql.AnyResult{attached}, nil
		}
		work := func(context.Context) (dagql.AnyResult, error) {
			signalWork.Do(func() { close(workStarted) })
			<-proceedWork
			return dagql.NewResultForCall(&attachingSharedWorkValue{String: "shared", attach: attachNested}, shared)
		}

		type outcome struct {
			res dagql.AnyResult
			err error
		}
		producerDone := make(chan error, 1)
		waiterDone, readerDone := make(chan outcome, 1), make(chan outcome, 1)
		var calls sync.WaitGroup
		t.Cleanup(func() {
			releaseWork()
			releaseAttach()
			cancelProducer()
			calls.Wait()
		})

		calls.Add(1)
		go func() {
			defer calls.Done()
			_, err := cache.GetOrInitCall(producerCtx, producer.sessionID, dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
			producerDone <- err
		}()
		receiveWithin(t, workStarted, "the shared work to start")

		calls.Add(1)
		go func() {
			defer calls.Done()
			res, err := cache.GetOrInitCall(waiterCtx, "waiter", dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
			waiterDone <- outcome{res, err}
		}()
		// 1. The waiter has joined the shared call.
		awaitCallWaiters(t, cache, sharedKey, 2)

		// 2. The producer's session is marked removed, as removeDaggerSession
		// begins, and its own request goes away.
		producer.markSessionRemoved()
		producer.beginClientScopeTeardown()
		cancelProducer()
		require.ErrorIs(t, receiveWithin(t, producerDone, "the producer's request to end"), context.Canceled)
		drained := make(chan error, 1)
		go func() { drained <- producer.waitForClientScopeDrain(t.Context()) }()
		producerScope.Lease().Release()
		producerTransport.Lease().Release()

		// 3. The shared work succeeds, and the waiter publishes its result under
		// the producer's scope.
		releaseWork()
		receiveWithin(t, attachStarted, "the result's publication to start")

		// 4. Another session finds the result while its publication is open,
		// and waits on it.
		calls.Add(1)
		go func() {
			defer calls.Done()
			res, err := cache.GetOrInitCall(readerCtx, "reader", dag, &dagql.CallRequest{ResultCall: shared}, work)
			readerDone <- outcome{res, err}
		}()
		synctest.Wait()

		// 5. The publication makes its nested call.
		releaseAttach()
		got := receiveWithin(t, waiterDone, "the waiter's call to return")
		require.NoError(t, got.err, "the waiter's session is alive; the shared call it joined must not fail with another session's closure")
		require.Equal(t, dagql.String("shared"), got.res.Unwrap().(*attachingSharedWorkValue).String)
		got = receiveWithin(t, readerDone, "the reader's call to return")
		require.NoError(t, got.err, "the reader's session is alive; the result it waited on must not fail it with another session's closure")
		require.Equal(t, dagql.String("shared"), got.res.Unwrap().(*attachingSharedWorkValue).String)
		nestedMu.Lock()
		require.NotEmpty(t, nestedSessions)
		require.NotContains(t, nestedSessions, "producer", "no publication succeeded under the removed producer's scope")
		nestedMu.Unlock()
		require.NoError(t, receiveWithin(t, drained, "the producer's scopes to drain"))
	})
}

// The publication of a shared call's result can publish more results: a value
// may attach a dependency it has just built, which the cache publishes with
// it under the same scope. When that dependency's own publication fails
// because the producer's session is removed, a session that found the
// dependency while its publication was open, and waits on it, must not get
// the failure, and the waiter on the shared call must still get its value.
func TestSharedCallDependencyPublicationSurvivesItsProducersSessionRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer, producerScope, producerTransport := sharedWorkTestSession(t, "producer")
		_, waiterScope, _ := sharedWorkTestSession(t, "waiter")
		_, readerScope, _ := sharedWorkTestSession(t, "reader")

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
		readerCtx := scoped(t.Context(), readerScope)

		const sharedKey = "shared-work"
		shared := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "sharedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}
		dependency := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "dependency", Type: dagql.NewResultCallType(dagql.String("").Type())}
		nested := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "nestedWork", Type: dagql.NewResultCallType(dagql.String("").Type())}

		workStarted, proceedWork := make(chan struct{}), make(chan struct{})
		attachStarted, proceedAttach := make(chan struct{}), make(chan struct{})
		releaseWork := sync.OnceFunc(func() { close(proceedWork) })
		releaseAttach := sync.OnceFunc(func() { close(proceedAttach) })
		var signalWork, signalAttach sync.Once
		var nestedMu sync.Mutex
		var nestedSessions []string
		// The dependency's publication makes a nested call, under the
		// publication's context.
		attachNested := func(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
			signalAttach.Do(func() { close(attachStarted) })
			<-proceedAttach
			md, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return nil, err
			}
			dep, err := cache.GetOrInitCall(ctx, md.SessionID, dag, &dagql.CallRequest{ResultCall: nested, ConcurrencyKey: "nested-work"}, func(context.Context) (dagql.AnyResult, error) {
				nestedMu.Lock()
				nestedSessions = append(nestedSessions, md.SessionID)
				nestedMu.Unlock()
				return dagql.NewResultForCall(dagql.String("nested"), nested)
			})
			if err != nil {
				return nil, err
			}
			attached, err := attach(dep)
			if err != nil {
				return nil, err
			}
			return []dagql.AnyResult{attached}, nil
		}
		buildDependency := func(context.Context) (dagql.AnyResult, error) {
			return dagql.NewResultForCall(&attachingSharedWorkValue{String: "dependency", attach: attachNested}, dependency)
		}
		// The shared call's result attaches a dependency it has just built.
		attachDependency := func(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
			dep, err := buildDependency(ctx)
			if err != nil {
				return nil, err
			}
			attached, err := attach(dep)
			if err != nil {
				return nil, err
			}
			return []dagql.AnyResult{attached}, nil
		}
		work := func(context.Context) (dagql.AnyResult, error) {
			signalWork.Do(func() { close(workStarted) })
			<-proceedWork
			return dagql.NewResultForCall(&attachingSharedWorkValue{String: "shared", attach: attachDependency}, shared)
		}

		type outcome struct {
			res dagql.AnyResult
			err error
		}
		producerDone := make(chan error, 1)
		waiterDone, readerDone := make(chan outcome, 1), make(chan outcome, 1)
		var calls sync.WaitGroup
		t.Cleanup(func() {
			releaseWork()
			releaseAttach()
			cancelProducer()
			calls.Wait()
		})

		calls.Add(1)
		go func() {
			defer calls.Done()
			_, err := cache.GetOrInitCall(producerCtx, producer.sessionID, dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
			producerDone <- err
		}()
		receiveWithin(t, workStarted, "the shared work to start")

		calls.Add(1)
		go func() {
			defer calls.Done()
			res, err := cache.GetOrInitCall(waiterCtx, "waiter", dag, &dagql.CallRequest{ResultCall: shared, ConcurrencyKey: sharedKey}, work)
			waiterDone <- outcome{res, err}
		}()
		// 1. The waiter has joined the shared call.
		awaitCallWaiters(t, cache, sharedKey, 2)

		// 2. The producer's session is marked removed, as removeDaggerSession
		// begins, and its own request goes away.
		producer.markSessionRemoved()
		producer.beginClientScopeTeardown()
		cancelProducer()
		require.ErrorIs(t, receiveWithin(t, producerDone, "the producer's request to end"), context.Canceled)
		drained := make(chan error, 1)
		go func() { drained <- producer.waitForClientScopeDrain(t.Context()) }()
		producerScope.Lease().Release()
		producerTransport.Lease().Release()

		// 3. The shared work succeeds, and the waiter publishes its result and
		// the dependency under the producer's scope.
		releaseWork()
		receiveWithin(t, attachStarted, "the dependency's publication to start")

		// 4. Another session finds the dependency while its publication is
		// open, and waits on it.
		calls.Add(1)
		go func() {
			defer calls.Done()
			res, err := cache.GetOrInitCall(readerCtx, "reader", dag, &dagql.CallRequest{ResultCall: dependency}, buildDependency)
			readerDone <- outcome{res, err}
		}()
		synctest.Wait()

		// 5. The dependency's publication makes its nested call.
		releaseAttach()
		got := receiveWithin(t, readerDone, "the reader's call to return")
		require.NoError(t, got.err, "the reader's session is alive; the dependency it waited on must not fail it with another session's closure")
		require.Equal(t, dagql.String("dependency"), got.res.Unwrap().(*attachingSharedWorkValue).String)
		got = receiveWithin(t, waiterDone, "the waiter's call to return")
		require.NoError(t, got.err, "the waiter's session is alive; the shared call it joined must not fail with another session's closure")
		require.Equal(t, dagql.String("shared"), got.res.Unwrap().(*attachingSharedWorkValue).String)
		nestedMu.Lock()
		require.NotEmpty(t, nestedSessions)
		require.NotContains(t, nestedSessions, "producer", "no publication succeeded under the removed producer's scope")
		nestedMu.Unlock()
		require.NoError(t, receiveWithin(t, drained, "the producer's scopes to drain"))
	})
}
