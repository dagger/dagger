package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/containerd/containerd/v2/core/content"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func (transferTestCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	var p transferTestValue
	if err := json.Unmarshal(v.Payload, &p); err != nil {
		return nil, err
	}
	snapshot := ""
	for _, link := range v.SnapshotLinks {
		if link.Role == "snapshot" {
			snapshot = link.RefKey
		}
	}
	return []PartProbe{{Descriptor: PartDescriptor{SnapshotID: snapshot, Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, Absent: p.Text == "ready"}, LocalComplete: p.Text == "ready" || snapshot != "", HasProducer: p.Text != "ready"}}, nil
}
func (transferTestCodec) PreparePartRecord(receiver, source PersistedRecord, descriptor PartDescriptor, _ PersistedPartAddress) (PersistedRecord, error) {
	if descriptor.SnapshotID != "" {
		receiver.Envelope.ObjectJSON = json.RawMessage(`{"text":"snapshot"}`)
		receiver.SnapshotLinks = []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: descriptor.SnapshotID}}
		return receiver, nil
	}
	receiver.Envelope.ObjectJSON = append([]byte(nil), source.Envelope.ObjectJSON...)
	return receiver, nil
}
func partTestEquivalent(t *testing.T, ctx context.Context, c *Cache, receiver, donor AnyResult) {
	t.Helper()
	dig, err := receiver.cacheSharedResult().loadResultCall().deriveRecipeDigest(c)
	require.NoError(t, err)
	c.egraphMu.Lock()
	c.addResultDigestPostingLocked(donor.cacheSharedResult().id, dig.String(), resultDigestPostingExact)
	c.egraphMu.Unlock()
}
func TestPartSourceSelection(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "ready"})
	partTestEquivalent(t, ctx, c, receiver, donor)
	leaf := persistedListTestResult(t, ctx, c, srv, "leaf", String("owned"))
	transferTestOffer(t, c, ctx, receiver, leaf)
	before := receiver.cacheSharedResult().incomingOwnershipCount
	source, err := c.AcquireEquivalentPartSource(ctx, receiver, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.NotNil(t, source)
	require.Equal(t, PartReady, source.Readiness())
	require.Same(t, donor.cacheSharedResult(), source.source)
	require.NoError(t, source.Release(ctx))
	require.NoError(t, source.Release(ctx))
	require.Equal(t, before, receiver.cacheSharedResult().incomingOwnershipCount)
	source, err = c.AcquireEquivalentPartSource(ctx, donor, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.Same(t, donor.cacheSharedResult(), source.source)
	require.NoError(t, source.Release(ctx))
}
func TestPartOfferSessionAdmission(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	leaf := persistedListTestResult(t, ctx, c, srv, "leaf", String("socket"))
	c.egraphMu.Lock()
	leaf.cacheSharedResult().sessionResourceHandle = "socket"
	_, err := c.recomputeRequiredSessionResourcesLocked(leaf.cacheSharedResult())
	c.egraphMu.Unlock()
	require.NoError(t, err)
	transferTestOffer(t, c, ctx, receiver, leaf)
	source, err := c.AcquireEquivalentPartSource(ctx, receiver, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.Nil(t, source)
	session, err := partSession(ctx)
	require.NoError(t, err)
	require.NoError(t, c.BindSessionResource(ctx, session, "client", "socket", new(int)))
	source, err = c.AcquireEquivalentPartSource(ctx, receiver, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.NotNil(t, source)
	require.Equal(t, PartDownloadable, source.Readiness())
	require.Nil(t, source.source)
	c.egraphMu.RLock()
	require.Nil(t, receiver.cacheSharedResult().requiredSessionResources)
	c.egraphMu.RUnlock()
	require.NoError(t, source.Release(ctx))
}
func TestReadyPartReceipt(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "ready"})
	partTestEquivalent(t, ctx, c, receiver, donor)
	record, err := c.CapturePersistedRecord(ctx, receiver)
	require.NoError(t, err)
	row := receiver.cacheSharedResult()
	row.payloadMu.Lock()
	row.self = nil
	row.hasValue = false
	row.persistedEnvelope = &record.Envelope
	row.payloadRevision++
	row.payloadMu.Unlock()
	address := PersistedPartAddress{Part: "snapshot"}
	barrier := make(chan struct{})
	receipts := make(chan *ReadyPartReceipt, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.RunLazyTask(ctx, receiver, "obtain:snapshot", LazyTaskSpec{OwnerSyncReady: barrier, Body: func(ctx context.Context) error {
			source, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
			if err != nil {
				return err
			}
			permit, _, err := c.TryAcquire(ctx, receiver, address, PartTaskFromContext(ctx))
			if err != nil {
				return err
			}
			p, err := c.PrepareReadyPart(ctx, receiver, source, permit)
			if err != nil {
				return err
			}
			receipt, outcome, err := c.CommitReadyPart(ctx, p)
			if outcome != PartInstalled {
				return errors.New("publication refused")
			}
			receipts <- receipt
			return err
		}})
	}()
	var receipt *ReadyPartReceipt
	select {
	case receipt = <-receipts:
	case err := <-done:
		require.NoError(t, err)
		t.Fatal("missing receipt")
	}
	require.False(t, receipt.task.settled.Load())
	require.NoError(t, c.FinishReadyPart(ctx, receipt))
	require.NoError(t, <-done)
	require.NoError(t, receipt.release(ctx))
	require.True(t, receipt.task.settled.Load())
	require.Contains(t, string(row.loadPayloadState().persistedEnvelope.ObjectJSON), "ready")
}

func TestPartSettlementRetiresReplacement(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	first := persistedListTestResult(t, ctx, c, srv, "first", String("one"))
	second := persistedListTestResult(t, ctx, c, srv, "second", String("two"))
	transferTestOffer(t, c, ctx, receiver, first)
	address := PersistedPartAddress{Part: "snapshot"}
	row := receiver.cacheSharedResult()
	gate := row.partGate.loadOrCreate()
	task := &PartTaskToken{row: row, generation: 1}
	key, _ := partAddressKey(address)
	c.egraphMu.Lock()
	gate.mu.Lock()
	gate.outputs[key] = partOutputState{phase: PartOutputInstalled, task: task, installation: 9}
	record := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory"}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(second.cacheSharedResult().id)}}}
	owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
	require.NoError(t, err)
	queue, err := c.replacePartOfferLocked(ctx, row, address, &partOffer{record: record, owner: owner})
	require.NoError(t, err)
	gate.mu.Unlock()
	callbacks, err := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	require.NoError(t, err)
	require.NoError(t, runOnReleaseFuncs(ctx, callbacks))
	require.NoError(t, c.settlePart(ctx, row, address, task, 9))
	require.NoError(t, c.settlePart(ctx, row, address, task, 9))
	c.egraphMu.RLock()
	require.Empty(t, row.partOffers)
	require.Empty(t, c.offerOwners)
	c.egraphMu.RUnlock()
	gate.mu.Lock()
	require.Equal(t, PartComplete, gate.outputs[key].phase)
	gate.mu.Unlock()
}

func TestPartDecisionFinalSource(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	address := PersistedPartAddress{Part: "snapshot"}
	require.NoError(t, c.RunLazyTask(ctx, receiver, "producer:whole", LazyTaskSpec{Body: func(ctx context.Context) error {
		task := PartTaskFromContext(ctx)
		drain, outcome, err := c.PrepareOriginal(ctx, receiver, ProducerAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, task)
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		require.NoError(t, drain.Wait(ctx))
		scan, err := c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
		require.NoError(t, err)
		require.NotNil(t, scan.NoSource)
		donor := persistedListTestResult(t, ctx, c, srv, "late", &transferTestValue{Text: "ready"})
		partTestEquivalent(t, ctx, c, receiver, donor)
		_, outcome, err = c.BeginOriginal(ctx, scan.NoSource)
		require.NoError(t, err)
		require.Equal(t, GateReselect, outcome)
		scan, err = c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
		require.NoError(t, err)
		require.NotNil(t, scan.Source)
		require.Equal(t, PartReady, scan.Source.Readiness())
		require.NoError(t, scan.Source.Release(ctx))
		return nil
	}}))
}

type partAvailabilityHook struct {
	once sync.Once
	fn   func()
}

func (h *partAvailabilityHook) Available(PersistedPartOffer, int64) bool {
	h.once.Do(h.fn)
	return true
}
func (*partAvailabilityHook) Provider(context.Context, PersistedPartOffer, *PartDemandState) content.InfoReaderProvider {
	panic("probe must not open provider")
}

func TestPartSourceScanFailureReleasesWinner(t *testing.T) {
	for _, mode := range []string{"ready", "chain"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			text := "pending"
			if mode == "ready" {
				text = "ready"
			}
			winner := persistedListTestResult(t, ctx, c, srv, "winner", &transferTestValue{Text: text})
			failure := errors.New("unselected candidate final release")
			releases := 0
			loser := persistedListTestResult(t, ctx, c, srv, "loser", &transferTestValue{Text: "pending", release: func(context.Context) error { releases++; return failure }})
			partTestEquivalent(t, ctx, c, receiver, winner)
			partTestEquivalent(t, ctx, c, receiver, loser)
			address := PersistedPartAddress{Part: "snapshot"}
			var winnerOwner *offerOwner
			c.egraphMu.Lock()
			for _, row := range []*sharedResult{winner.cacheSharedResult(), loser.cacheSharedResult()} {
				owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{})
				require.NoError(t, err)
				require.NoError(t, c.attachPartOfferLocked(row, address, &partOffer{record: PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory"}}, owner: owner}))
				if row == winner.cacheSharedResult() {
					winnerOwner = owner
				}
			}
			c.egraphMu.Unlock()
			c.SetPartContentSource(&partAvailabilityHook{fn: func() {
				require.NoError(t, c.ReleaseSession(ctx, "test-session"))
				_, err := c.removePersistedEdge(ctx, loser.cacheSharedResult().id)
				require.NoError(t, err, "the scan still holds the loser")
			}})
			source, _, err := c.scanPartSources(ctx, receiver, address, nil)
			require.ErrorIs(t, err, failure)
			require.Nil(t, source, "no winning ownership escapes an error")
			require.Equal(t, 1, releases)
			c.egraphMu.RLock()
			require.Nil(t, c.resultsByID[loser.cacheSharedResult().id])
			require.Equal(t, winnerOwner.slots, winnerOwner.holds)
			// Only the durable edge remains; both probe and selected Ready holds ended.
			require.EqualValues(t, 1, winner.cacheSharedResult().incomingOwnershipCount)
			c.egraphMu.RUnlock()
		})
	}
}

func TestPartNativeCompletionRetiresOffer(t *testing.T) {
	ctx, c := newPartsTestCache(t, nil)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	obj := &cacheTestPartsObject{resolveFn: partsTestDirectResolve}
	var receiver ObjectResult[*cacheTestPartsObject]
	bodies := 0
	address := PersistedPartAddress{Part: partsTestPartFS}
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{partsTestGroupOut: func(ctx context.Context) error {
		return c.partHostFor(receiver.cacheSharedResult()).RunNative(ctx, partsTestGroupOut, []PartKey{partsTestPartFS}, func(ctx context.Context) error {
			bodies++
			// Offer arrival after native admission cannot change its running route.
			c.egraphMu.Lock()
			defer c.egraphMu.Unlock()
			owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{})
			if err != nil {
				return err
			}
			return c.attachPartOfferLocked(receiver.cacheSharedResult(), address, &partOffer{record: PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory"}}, owner: owner})
		})
	}}
	receiver = newPartsTestResult(t, c, ctx, obj)
	require.NoError(t, c.EvaluateParts(ctx, receiver, partsTestPartFS))
	require.NoError(t, c.EvaluateParts(ctx, receiver, partsTestPartFS))
	require.Equal(t, 1, bodies)
	row := receiver.cacheSharedResult()
	c.egraphMu.RLock()
	require.Empty(t, row.partOffers)
	require.Empty(t, c.offerOwners)
	c.egraphMu.RUnlock()
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	key, _ := partAddressKey(address)
	require.Equal(t, PartComplete, gate.outputs[key].phase)
	gate.mu.Unlock()
}
