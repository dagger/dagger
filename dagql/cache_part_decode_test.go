package dagql

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestPartDecodeBeforeExternalFinish(t *testing.T) {
	store := testutil.NewStore(t)
	ref, _ := store.Build(t, nil, "payload", "protected before Finish")
	ctx, c, srv := transferTestCache(t)
	c.snapshotManager = store.Manager
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: ref.SnapshotID()}}})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	partTestEquivalent(t, ctx, c, receiver, donor)
	record, err := c.CapturePersistedRecord(ctx, receiver)
	require.NoError(t, err)
	row := receiver.cacheSharedResult()
	row.payloadMu.Lock()
	row.hasValue = false
	row.self = nil
	row.persistedEnvelope = &record.Envelope
	row.imported = true
	row.payloadRevision++
	row.payloadMu.Unlock()
	address := PersistedPartAddress{Part: "snapshot"}
	barrier := make(chan struct{})
	receipts := make(chan *ReadyPartReceipt, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.RunLazyTask(ctx, receiver, "obtain:split-decode", LazyTaskSpec{OwnerSyncReady: barrier, Body: func(ctx context.Context) error {
			source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
			if err != nil {
				return err
			}
			permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
			if err != nil {
				return err
			}
			prepared, err := c.PrepareReadyPart(ctx, receiver, source, permit)
			if err != nil {
				return err
			}
			receipt, _, err := c.CommitReadyPart(ctx, prepared)
			if receipt != nil {
				receipts <- receipt
			}
			return err
		}})
	}()
	var receipt *ReadyPartReceipt
	select {
	case receipt = <-receipts:
	case err := <-done:
		require.NoError(t, err)
		t.Fatal("no receipt")
	}
	require.False(t, receipt.task.settled.Load())
	require.Empty(t, row.loadSnapshotOwnerLinks(), "Commit changes desired intent before applied links")
	transferDecodeHooks.Store(uint64(row.id), func(ctx context.Context, dec *PersistDecodeContext, value *transferTestValue) error {
		require.True(t, dec.Imported())
		roles, err := dec.SnapshotRoles(ctx)
		require.NoError(t, err)
		require.Equal(t, []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: ref.SnapshotID()}}, roles)
		return nil
	})
	defer transferDecodeHooks.Delete(uint64(row.id))
	loaded, err := c.LoadResultByResultID(ctx, "", srv, uint64(row.id))
	require.NoError(t, err)
	require.Equal(t, "snapshot", loaded.Unwrap().(*transferTestValue).Text)
	require.Len(t, row.loadSnapshotOwnerLinks(), 1, "decode may synchronize leases independently")
	gate := row.partGate.gate.Load()
	key, _ := partAddressKey(address)
	gate.mu.Lock()
	require.Equal(t, PartOutputInstalled, gate.outputs[key].phase)
	require.Same(t, receipt.task, gate.outputs[key].task)
	gate.mu.Unlock()
	require.False(t, receipt.task.settled.Load())
	require.NoError(t, c.FinishReadyPart(ctx, receipt))
	require.NoError(t, <-done)
	gate.mu.Lock()
	require.Equal(t, PartComplete, gate.outputs[key].phase)
	gate.mu.Unlock()
	encoded, err := c.CapturePersistedRecord(ctx, loaded)
	require.NoError(t, err)
	require.JSONEq(t, string(json.RawMessage(`{"text":"snapshot"}`)), string(encoded.Envelope.ObjectJSON))
}

func TestPartDecodeLosesToInstalledRevision(t *testing.T) {
	store := testutil.NewStore(t)
	ref, _ := store.Build(t, nil, "payload", "new installation")
	ctx, c, srv := transferTestCache(t)
	c.snapshotManager = store.Manager
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: ref.SnapshotID()}}})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	partTestEquivalent(t, ctx, c, receiver, donor)
	partEncodedReceiver(t, ctx, c, receiver)
	row := receiver.cacheSharedResult()
	row.imported = true
	entered, resume := make(chan struct{}), make(chan struct{})
	var losing, winning, calls atomic.Int32
	transferDecodeHooks.Store(uint64(row.id), func(ctx context.Context, dec *PersistDecodeContext, value *transferTestValue) error {
		require.True(t, dec.Imported())
		if calls.Add(1) == 1 {
			value.release = func(context.Context) error { losing.Add(1); return nil }
			close(entered)
			<-resume
		} else {
			roles, err := dec.SnapshotRoles(ctx)
			require.NoError(t, err)
			require.Equal(t, []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: ref.SnapshotID()}}, roles)
			value.release = func(context.Context) error { winning.Add(1); return nil }
		}
		return nil
	})
	defer transferDecodeHooks.Delete(uint64(row.id))
	loaded := make(chan AnyResult, 1)
	errors := make(chan error, 1)
	go func() {
		result, err := c.LoadResultByResultID(ctx, "", srv, uint64(row.id))
		loaded <- result
		errors <- err
	}()
	waitLazyRetrySignal(t, entered, "old decode")
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:decode-race", LazyTaskSpec{Body: func(ctx context.Context) error {
		address := PersistedPartAddress{Part: "snapshot"}
		source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
		if err != nil {
			return err
		}
		permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
		if err != nil {
			return err
		}
		return c.InstallReadyPart(ctx, receiver, source, permit)
	}}))
	close(resume)
	require.NoError(t, waitLazyRetryError(t, errors, "new decode"))
	require.Equal(t, "snapshot", (<-loaded).Unwrap().(*transferTestValue).Text)
	require.EqualValues(t, 2, calls.Load())
	require.EqualValues(t, 1, losing.Load())
	require.Zero(t, winning.Load())
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err := c.removePersistedEdge(ctx, row.id)
	require.NoError(t, err)
	require.EqualValues(t, 1, winning.Load())
}
