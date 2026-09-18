package dagql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

var partLazyPreparationHooks sync.Map

func (transferTestCodec) PrepareLazyOperation(ctx context.Context, dec *PersistDecodeContext, _ PersistedRecord, _ LazyOperationRoute) (LazyOperationInvocation, error) {
	if hook, ok := partLazyPreparationHooks.Load(dec.ResultID()); ok {
		return hook.(func(context.Context) (LazyOperationInvocation, error))(ctx)
	}
	return nil, errors.New("test operation preparation hook missing")
}

type boundaryLazyOperation struct{ runs, releases atomic.Int32 }

func (p *boundaryLazyOperation) Run(context.Context) error {
	p.runs.Add(1)
	return errors.New("unexpected private body")
}
func (p *boundaryLazyOperation) Capture(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	return PersistedObjectEncoding{}, errors.New("unexpected private capture")
}
func (p *boundaryLazyOperation) Release(context.Context) error { p.releases.Add(1); return nil }

func partEncodedReceiver(t *testing.T, ctx context.Context, c *Cache, receiver AnyResult) {
	t.Helper()
	record, err := c.CapturePersistedRecord(ctx, receiver)
	require.NoError(t, err)
	row := receiver.cacheSharedResult()
	row.payloadMu.Lock()
	row.hasValue, row.self = false, nil
	row.persistedEnvelope = &record.Envelope
	row.payloadRevision++
	row.payloadMu.Unlock()
}

type boundaryPinManager struct {
	snapshots.SnapshotManager
	afterPin func()
	pins     atomic.Int32
	releases atomic.Int32
}

func (m *boundaryPinManager) PinSnapshot(ctx context.Context, id string) (snapshots.ImmutableRef, error) {
	ref, err := m.SnapshotManager.PinSnapshot(ctx, id)
	if err == nil {
		m.pins.Add(1)
		if m.afterPin != nil {
			m.afterPin()
		}
		ref = &boundaryPinRef{ImmutableRef: ref, released: &m.releases}
	}
	return ref, err
}

type boundaryPinRef struct {
	snapshots.ImmutableRef
	released *atomic.Int32
}

func (r *boundaryPinRef) Release(ctx context.Context) error {
	err := r.ImmutableRef.Release(ctx)
	if err == nil {
		r.released.Add(1)
	}
	return err
}

func TestPartReadyPreparationBoundaries(t *testing.T) {
	for _, mode := range []string{"cancel-during-pin", "missing-local-descriptor"} {
		t.Run(mode, func(t *testing.T) {
			store := testutil.NewStore(t)
			ref, _ := store.Build(t, nil, "payload", "ready source")
			ctx, c, srv := transferTestCache(t)
			manager := &boundaryPinManager{SnapshotManager: store.Manager}
			c.snapshotManager = manager
			value := &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: ref.SnapshotID()}}}
			donor := persistedListTestResult(t, ctx, c, srv, "donor", value)
			if mode == "missing-local-descriptor" {
				// Model a descriptor whose local backing disappeared after ordinary
				// attachment. The ranking pass must not substitute an operation.
				value.links = []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "missing-ready-snapshot"}}
				value.rev.Add(1)
			}
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			partTestEquivalent(t, c, receiver, donor)
			partEncodedReceiver(t, ctx, c, receiver)
			before := ownershipCounts(c, receiver.cacheSharedResult(), donor.cacheSharedResult())
			released := armLazyAttemptReleased(c)
			// The Body runs off the test goroutine, so it reports through its
			// error rather than failing the test itself.
			require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:prepare-boundary", LazyTaskSpec{Body: func(ctx context.Context) error {
				address := PersistedPartAddress{Part: "snapshot"}
				source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
				if err != nil {
					return fmt.Errorf("acquire source: %w", err)
				}
				if source.Readiness() != PartReady {
					return fmt.Errorf("pure ranking cannot open storage: readiness %v", source.Readiness())
				}
				permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
				if err != nil {
					return fmt.Errorf("acquire permit: %w", err)
				}
				prepareCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				if mode == "cancel-during-pin" {
					manager.afterPin = cancel
				}
				prepared, err := c.PrepareReadyPart(prepareCtx, receiver, source, permit)
				if mode == "missing-local-descriptor" {
					if err == nil || !strings.Contains(err.Error(), "missing-ready-snapshot") {
						return fmt.Errorf("prepare with a missing descriptor: want the descriptor named, got %v", err)
					}
					if prepared != nil {
						return fmt.Errorf("prepare with a missing descriptor returned a preparation")
					}
					return nil
				}
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						return fmt.Errorf("prepare: want cancellation, got %w", err)
					}
					return nil
				}
				receipt, outcome, err := c.CommitReadyPart(prepareCtx, prepared)
				if !errors.Is(err, context.Canceled) {
					return fmt.Errorf("commit: want cancellation, got %w", err)
				}
				if outcome != PartInstallRefused || receipt != nil {
					return fmt.Errorf("commit after cancellation: outcome %v, receipt %v", outcome, receipt)
				}
				return nil
			}}))
			require.Empty(t, receiver.cacheSharedResult().loadSnapshotOwnerLinks())
			require.Equal(t, manager.pins.Load(), manager.releases.Load())
			waitLazyAttemptReleased(t, released)
			require.Equal(t, before, ownershipCounts(c, receiver.cacheSharedResult(), donor.cacheSharedResult()))
		})
	}
}

func TestPartDecisionPreparationArrival(t *testing.T) {
	for _, mode := range []string{"ready", "chain", "second-refusal"} {
		t.Run(mode, func(t *testing.T) {
			a, b := testutil.NewStore(t), testutil.NewStore(t)
			ref, _ := a.Build(t, nil, "payload", "late source bytes")
			chain, err := ref.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
			require.NoError(t, err)
			defer func() { require.NoError(t, chain.Release(context.Background())) }()
			ctx, c, srv := transferTestCache(t)
			c.snapshotManager = b.Manager
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			partEncodedReceiver(t, ctx, c, receiver)
			address := PersistedPartAddress{Part: "snapshot"}
			row := receiver.cacheSharedResult()
			operation := new(boundaryLazyOperation)
			var manager *boundaryPinManager
			partLazyPreparationHooks.Store(uint64(row.id), func(context.Context) (LazyOperationInvocation, error) {
				if mode == "chain" {
					record := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.Layers, RenewalKey: "late-chain"}}
					c.egraphMu.Lock()
					owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
					require.NoError(t, err)
					require.NoError(t, c.attachPartOfferLocked(row, address, &partOffer{record: record, owner: owner}))
					c.egraphMu.Unlock()
					c.SetPartContentSource(lifetimeChainSource{chain.Provider})
				} else {
					local, _ := b.Build(t, nil, "payload", "late source bytes")
					donor := persistedListTestResult(t, ctx, c, srv, "late-donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: local.SnapshotID()}}})
					partTestEquivalent(t, c, receiver, donor)
					if mode == "second-refusal" {
						manager = &boundaryPinManager{SnapshotManager: b.Manager, afterPin: func() {
							row.payloadMu.Lock()
							row.payloadRevision++
							row.payloadMu.Unlock()
						}}
						c.snapshotManager = manager
					}
				}
				return operation, nil
			})
			defer partLazyPreparationHooks.Delete(uint64(row.id))
			err = c.runLazyOperationDecision(ctx, receiver, address, LazyOperationRoute{Group: LazyGroupAddress{Group: LazyGroupWhole}, WriteSet: []PersistedPartAddress{address}, HasLazyOperation: true}, &PartDemandState{})
			if mode == "second-refusal" {
				require.ErrorIs(t, err, ErrPartReselect)
				require.EqualValues(t, 2, manager.pins.Load())
				require.EqualValues(t, 2, manager.releases.Load())
				require.Empty(t, row.loadSnapshotOwnerLinks())
			} else {
				require.NoError(t, err)
				links := row.loadSnapshotOwnerLinks()
				require.Len(t, links, 1)
				opened, err := b.Manager.GetBySnapshotID(ctx, links[0].RefKey)
				require.NoError(t, err)
				testutil.CheckFile(t, opened, "payload", "late source bytes")
				require.NoError(t, opened.Release(ctx))
			}
			require.Zero(t, operation.runs.Load())
			require.EqualValues(t, 1, operation.releases.Load(), "prepared private shell released without Running")
		})
	}
}

func TestPartDecisionInlineAdmissionAndPendingSync(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "ready"})
	partTestEquivalent(t, c, receiver, donor)
	partEncodedReceiver(t, ctx, c, receiver)
	fs, meta := PersistedPartAddress{Part: "snapshot"}, PersistedPartAddress{Part: "execMeta"}
	entered, leave, syncReady := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var receipt *ReadyPartReceipt
	var token *PartTaskToken
	done := make(chan error, 1)
	go func() {
		done <- c.RunLazyTask(ctx, receiver, "lazy:decision", LazyTaskSpec{OwnerSyncReady: syncReady, Body: func(ctx context.Context) error {
			token = PartTaskFromContext(ctx)
			drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: "execOutputs"}, []PersistedPartAddress{fs, meta}, token)
			if err != nil {
				return err
			}
			if err = drain.Wait(ctx); err != nil {
				return err
			}
			scan, err := c.CheckPartSources(ctx, receiver, fs, drain, &PartDemandState{})
			if err != nil {
				return err
			}
			permit, _, err := c.TryAcquireForDecision(ctx, receiver, fs, drain, token)
			if err != nil {
				return err
			}
			prepared, err := c.PrepareReadyPart(ctx, receiver, scan.Source, permit)
			if err != nil {
				return err
			}
			receipt, _, err = c.CommitReadyPart(ctx, prepared)
			if err != nil {
				return err
			}
			close(entered)
			<-leave
			return nil
		}})
	}()
	waitLazyRetrySignal(t, entered, "inline source Commit")
	for _, noJoin := range []bool{false, true} {
		require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:execMeta", LazyTaskSpec{NoJoin: noJoin, Body: func(ctx context.Context) error {
			_, outcome, err := c.TryAcquire(ctx, receiver, meta, PartTaskFromContext(ctx))
			require.NoError(t, err)
			require.Equal(t, GateBusy, outcome)
			return nil
		}}))
	}
	require.ErrorIs(t, c.RunLazyTask(ctx, receiver, "lazy:decision", LazyTaskSpec{NoJoin: true}), ErrLazyTaskBusy)
	close(leave)
	require.Eventually(t, func() bool { return !token.active.Load() }, time.Second, time.Millisecond)
	require.False(t, token.settled.Load())
	require.NoError(t, c.RunLazyTask(ctx, receiver, "obtain:execMeta", LazyTaskSpec{NoJoin: true, Body: func(ctx context.Context) error {
		permit, outcome, err := c.TryAcquire(ctx, receiver, meta, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome, "sibling reopens while owning task awaits sync")
		permit.Release()
		return nil
	}}))
	require.NoError(t, c.FinishReadyPart(ctx, receipt))
	require.NoError(t, waitLazyRetryError(t, done, "decision Finish"))
}

func TestPartDecisionOfferAfterRunning(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	address := PersistedPartAddress{Part: "snapshot"}
	require.NoError(t, c.RunLazyTask(ctx, receiver, "lazy:running", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.NoError(t, drain.Wait(ctx))
		scan, err := c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
		require.NoError(t, err)
		original, outcome, err := c.BeginOriginal(ctx, scan.NoSource)
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		leaf := persistedListTestResult(t, ctx, c, srv, "late-owner", String("late"))
		transferTestOffer(t, c, ctx, receiver, leaf)
		source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
		require.NoError(t, err)
		require.NotNil(t, source)
		require.NoError(t, source.Release(ctx))
		_, outcome, err = c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateExecutionStarted, outcome)
		original.gate.mu.Lock()
		require.Equal(t, LazyEvaluationRunning, original.gate.groups[lazyGroupAddressKey(original.group)].phase)
		original.gate.mu.Unlock()
		return nil
	}}))
}
