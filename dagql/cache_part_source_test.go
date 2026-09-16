package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func (transferTestCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	var p transferTestValue
	if err := json.Unmarshal(v.Payload, &p); err != nil {
		return nil, err
	}
	return []PartProbe{{Descriptor: PartDescriptor{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, Absent: p.Text == "ready"}, LocalComplete: p.Text == "ready", HasProducer: p.Text != "ready"}}, nil
}
func (transferTestCodec) PreparePartRecord(receiver, source PersistedRecord, _ PartDescriptor, _ PersistedPartAddress) (PersistedRecord, error) {
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
