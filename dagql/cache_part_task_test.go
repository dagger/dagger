package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPartTaskBodyIsolation(t *testing.T) {
	var native, synthetic atomic.Int32
	ctx, c, _, receiver := newLazyRetryTestResult(t, func(context.Context) error { native.Add(1); return nil })
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:snapshot", LazyTaskSpec{Body: func(ctx context.Context) error {
		token := PartTaskFromContext(ctx)
		require.NotNil(t, token)
		require.NotZero(t, token.generation)
		synthetic.Add(1)
		return nil
	}}))
	require.EqualValues(t, 0, native.Load())
	require.EqualValues(t, 1, synthetic.Load())
	require.False(t, receiver.cacheSharedResult().lazyEvalComplete)
	require.ErrorContains(t, c.RunLazyTask(ctx, receiver, "acquire:snapshot", LazyTaskSpec{}), "missing supplied body")
	require.NoError(t, c.Evaluate(ctx, receiver))
	require.EqualValues(t, 1, native.Load())
}

func TestPartTaskContinuation(t *testing.T) {
	ctx, c, _, receiver := newLazyRetryTestResult(t, nil)
	var bodies, cleanups, settlements atomic.Int32
	failed := errors.New("post-publication cleanup")
	spec := LazyTaskSpec{Body: func(ctx context.Context) error {
		bodies.Add(1)
		PartTaskFromContext(ctx).installed.Store(&InstalledOutputs{})
		return nil
	}, AfterOwnerSync: func(context.Context) error {
		if cleanups.Add(1) == 1 {
			return failed
		}
		return nil
	}, Settled: func(context.Context) error { settlements.Add(1); return nil }}
	require.ErrorIs(t, c.RunLazyTask(ctx, receiver, "obtain:snapshot", spec), failed)
	refused := spec
	refused.NoJoin = true
	require.ErrorIs(t, c.RunLazyTask(ctx, receiver, "obtain:snapshot", refused), ErrLazyTaskBusy)
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:snapshot", LazyTaskSpec{}))
	require.EqualValues(t, 1, bodies.Load())
	require.EqualValues(t, 2, cleanups.Load())
	require.EqualValues(t, 1, settlements.Load())
}

func TestPartTaskNoJoinBarrier(t *testing.T) {
	ctx, c, _, receiver := newLazyRetryTestResult(t, nil)
	ready, bodyDone := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var cleaned atomic.Int32
	go func() {
		done <- c.RunLazyTask(ctx, receiver, "obtain:snapshot", LazyTaskSpec{
			Body: func(context.Context) error { close(bodyDone); return nil }, OwnerSyncReady: ready,
			AfterOwnerSync: func(context.Context) error { cleaned.Add(1); return nil },
		})
	}()
	waitLazyRetrySignal(t, bodyDone, "body")
	require.ErrorIs(t, c.RunLazyTask(ctx, receiver, "obtain:snapshot", LazyTaskSpec{NoJoin: true}), ErrLazyTaskBusy)
	require.Zero(t, cleaned.Load())
	close(ready)
	require.NoError(t, waitLazyRetryError(t, done, "bookkeeping"))
	require.EqualValues(t, 1, cleaned.Load())
}

func TestPartGateFiniteDrain(t *testing.T) {
	ctx, c, _, receiver := newLazyRetryTestResult(t, nil)
	fs := PersistedPartAddress{Part: "fs"}
	meta := PersistedPartAddress{Part: "execMeta"}
	admitted, release := make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- c.RunLazyTask(ctx, receiver, "obtain:fs", LazyTaskSpec{Body: func(ctx context.Context) error {
			permit, outcome, err := c.TryAcquire(ctx, receiver, fs, PartTaskFromContext(ctx))
			require.NoError(t, err)
			require.Equal(t, GateGranted, outcome)
			close(admitted)
			<-release
			permit.Release()
			permit.Release()
			return nil
		}})
	}()
	waitLazyRetrySignal(t, admitted, "acquisition permit")
	decisionStarted := make(chan struct{})
	decisionDone := make(chan error, 1)
	go func() {
		decisionDone <- c.RunLazyTask(ctx, receiver, "lazy:execOutputs", LazyTaskSpec{Body: func(ctx context.Context) error {
			token := PartTaskFromContext(ctx)
			drain, outcome, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: "execOutputs"}, []PersistedPartAddress{fs, meta}, token)
			require.NoError(t, err)
			require.Equal(t, GateGranted, outcome)
			close(decisionStarted)
			require.NoError(t, drain.Wait(ctx))
			permit, outcome, err := c.TryAcquireForDecision(ctx, receiver, meta, drain, token)
			require.NoError(t, err)
			require.Equal(t, GateGranted, outcome)
			permit.Release()
			return nil
		}})
	}()
	waitLazyRetrySignal(t, decisionStarted, "decision drain")
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:execMeta", LazyTaskSpec{Body: func(ctx context.Context) error {
		_, outcome, err := c.TryAcquire(ctx, receiver, meta, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateBusy, outcome)
		return nil
	}}))
	close(release)
	require.NoError(t, waitLazyRetryError(t, writerDone, "writer"))
	require.NoError(t, waitLazyRetryError(t, decisionDone, "decision"))
	// Body exit reopens admission even though this decision did not execute.
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:execMeta", LazyTaskSpec{Body: func(ctx context.Context) error {
		permit, outcome, err := c.TryAcquire(ctx, receiver, meta, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		permit.Release()
		return nil
	}}))
}
