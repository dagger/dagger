package dagql

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func testLiveOffer() PersistedPartOffer {
	return PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory", Path: "/"}}
}

func TestOfferPartsBeforeStart(t *testing.T) {
	for _, phase := range []LazyEvaluationPhase{LazyEvaluationOpen, LazyEvaluationPreparing, LazyEvaluationRunning, LazyEvaluationEvaluated} {
		t.Run([]string{"open", "preparing", "running", "evaluated"}[phase], func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			row := r.cacheSharedResult()
			gate := row.partGate.loadOrCreate()
			gate.groups[lazyGroupAddressKey(LazyGroupAddress{Group: "exec"})] = &partLazyEvaluationState{phase: phase, writeSet: []PersistedPartAddress{{Part: "snapshot"}}}
			out, err := c.OfferParts(ctx, r, []PersistedPartOffer{testLiveOffer()})
			require.NoError(t, err)
			if phase == LazyEvaluationRunning || phase == LazyEvaluationEvaluated {
				require.Equal(t, OfferExecutionStarted, out[0].Outcome)
				require.Empty(t, row.partOffers)
			} else {
				require.Equal(t, OfferAccepted, out[0].Outcome)
				require.Len(t, row.partOffers, 1)
			}
		})
	}
	for _, whole := range []bool{false, true} {
		ctx, c, srv := transferTestCache(t)
		r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
		group := LazyGroupKey("other")
		if whole {
			group = LazyGroupWhole
		}
		r.cacheSharedResult().partGate.loadOrCreate().groups[lazyGroupAddressKey(LazyGroupAddress{Group: group})] = &partLazyEvaluationState{phase: LazyEvaluationRunning, writeSet: []PersistedPartAddress{{Part: "other"}}}
		out, err := c.OfferParts(ctx, r, []PersistedPartOffer{testLiveOffer()})
		require.NoError(t, err)
		if whole {
			require.Equal(t, OfferExecutionStarted, out[0].Outcome)
		} else {
			require.Equal(t, OfferAccepted, out[0].Outcome)
		}
	}
}

func TestOfferPartsInvalidatesSourceCheck(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	address := testLiveOffer().Address
	require.NoError(t, c.RunLazyTask(ctx, r, "lazy:offer", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, outcome, err := c.PrepareOriginal(ctx, r, LazyGroupAddress{Group: "exec"}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		require.NoError(t, err)
		require.Equal(t, GateGranted, outcome)
		require.NoError(t, drain.Wait(ctx))
		scan, err := c.CheckPartSources(ctx, r, address, drain, &PartDemandState{})
		require.NoError(t, err)
		require.NotNil(t, scan.NoSource)
		out, err := c.OfferParts(ctx, r, []PersistedPartOffer{testLiveOffer()})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome)
		_, outcome, err = c.BeginOriginal(ctx, scan.NoSource)
		require.NoError(t, err)
		require.Equal(t, GateReselect, outcome)
		return nil
	}}))
}

func TestOfferPartsOwnership(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	dep := persistedListTestResult(t, ctx, c, srv, "dep", String("dep"))
	row := r.cacheSharedResult()
	offer := testLiveOffer()
	offer.Owner.DependencyIDs = []uint64{uint64(dep.cacheSharedResult().id), uint64(dep.cacheSharedResult().id)}
	offer.Value.Services = []TransferredServiceBinding{{ServiceResultID: uint64(dep.cacheSharedResult().id), Aliases: []string{"a"}}}
	blob := digest.FromString("content")
	offer.Chain.Layers = []snapshots.ExportLayer{{Descriptor: ocispec.Descriptor{Digest: blob, Size: 7, MediaType: "application/octet-stream"}}}
	base := dep.cacheSharedResult().incomingOwnershipCount
	out, err := c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	key, _ := partAddressKey(offer.Address)
	owner := row.partOffers[key].owner
	require.Equal(t, int64(1), owner.holds)
	require.Equal(t, base+1, dep.cacheSharedResult().incomingOwnershipCount)
	rev := row.transferRevision
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, rev, out[0].OfferRev)
	offer.Value.Services[0].Aliases[0] = "caller mutation"
	require.Equal(t, "a", row.partOffers[key].record.Value.Services[0].Aliases[0])
	offer.Value.Services[0].Aliases[0] = "a"
	// An address refresh preserves the exact same owner.
	offer.Chain.Addresses = map[digest.Digest]BlobAddress{blob: {URL: "https://example.invalid/blob"}}
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.True(t, out[0].Replaced)
	require.Same(t, owner, row.partOffers[key].owner)
	require.Equal(t, int64(1), owner.holds)
	require.Equal(t, base+1, dep.cacheSharedResult().incomingOwnershipCount)
	bad := testLiveOffer()
	bad.Owner.DependencyIDs = []uint64{uint64(row.id)}
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{offer, bad})
	require.ErrorContains(t, err, "cycle")
	require.Len(t, out, 2)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.Equal(t, OfferInvalid, out[1].Outcome)
	require.Same(t, owner, row.partOffers[key].owner)
}

func TestOfferPartsResourcesAndSettlement(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	dep := persistedListTestResult(t, ctx, c, srv, "dep", String("socket"))
	c.egraphMu.Lock()
	dep.cacheSharedResult().sessionResourceHandle = "socket"
	_, err := c.recomputeRequiredSessionResourcesLocked(dep.cacheSharedResult())
	c.egraphMu.Unlock()
	require.NoError(t, err)
	offer := testLiveOffer()
	offer.Owner.DependencyIDs = []uint64{uint64(dep.cacheSharedResult().id)}
	out, err := c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.Empty(t, r.cacheSharedResult().requiredSessionResources)
	source, err := c.AcquireEquivalentPartSource(ctx, r, offer.Address)
	require.NoError(t, err)
	require.Nil(t, source)
	session, err := partSession(ctx)
	require.NoError(t, err)
	require.NoError(t, c.BindSessionResource(ctx, session, "client", "socket", new(int)))
	source, err = c.AcquireEquivalentPartSource(ctx, r, offer.Address)
	require.NoError(t, err)
	require.NotNil(t, source)
	old := source.offerOwner
	replacement := testLiveOffer()
	replacement.Value.Path = "/replacement"
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{replacement})
	require.NoError(t, err)
	require.True(t, out[0].Replaced)
	require.Equal(t, int64(1), old.holds)
	row := r.cacheSharedResult()
	gate := row.partGate.loadOrCreate()
	key, _ := partAddressKey(offer.Address)
	token := &PartTaskToken{row: row, generation: 1}
	gate.outputs[key] = partOutputState{phase: PartOutputInstalled, task: token, installation: 1}
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAlreadyComplete, out[0].Outcome)
	require.NoError(t, c.settlePart(ctx, row, offer.Address, token, 1))
	require.NoError(t, c.settlePart(ctx, row, offer.Address, token, 1))
	require.Empty(t, row.partOffers)
	require.Equal(t, int64(1), old.holds)
	require.NoError(t, source.Release(ctx))
	require.Empty(t, c.offerOwners)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	out, err = c.OfferParts(canceled, r, []PersistedPartOffer{offer, offer})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, out, 2)
	for _, d := range out {
		require.Equal(t, OfferUnavailable, d.Outcome)
		require.True(t, errors.Is(d.Err, context.Canceled))
	}
}

func TestOfferPartsInlineAndReferencedRows(t *testing.T) {
	for _, separate := range []bool{false, true} {
		ctx, c, srv := transferTestCache(t)
		leaf := persistedListTestResult(t, ctx, c, srv, "leaf", &transferTestValue{Text: "pending"})
		list := persistedListTestResult(t, ctx, c, srv, "list", DynamicResultArrayOutput{Elem: &transferTestValue{}, Values: []AnyResult{leaf}})
		if !separate {
			// Attachment gives result handles separate rows. Construct the other
			// supported persisted representation explicitly: an inline object.
			record, err := c.CapturePersistedRecord(ctx, leaf)
			require.NoError(t, err)
			partEncodedReceiver(t, ctx, c, list)
			row := list.cacheSharedResult()
			row.payloadMu.Lock()
			record.Envelope.ResultID = 0
			row.persistedEnvelope.Items[0] = record.Envelope
			row.payloadRevision++
			row.payloadMu.Unlock()
		}
		offer := testLiveOffer()
		offer.Address.OutputPath = PersistedRefPath{}.Field("items").Index(0)
		out, err := c.OfferParts(ctx, list, []PersistedPartOffer{offer})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome)
		require.Equal(t, offer.Address, out[0].Address)
		row := list.cacheSharedResult()
		if separate {
			row = leaf.cacheSharedResult()
			offer.Address.OutputPath = nil
		}
		key, err := partAddressKey(offer.Address)
		require.NoError(t, err)
		require.Contains(t, row.partOffers, key)
	}
}

func TestOfferPartsClosesNativeAdmission(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	require.NoError(t, c.RunLazyTask(ctx, r, "lazy:late-native", LazyTaskSpec{Body: func(ctx context.Context) error {
		out, err := c.OfferParts(ctx, r, []PersistedPartOffer{testLiveOffer()})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome)
		err = c.partHostFor(r.cacheSharedResult()).RunNative(ctx, LazyGroupWhole, []PartKey{"snapshot"}, func(context.Context) error { t.Fatal("body entered after offer acceptance"); return nil })
		require.ErrorIs(t, err, ErrPartReselect)
		return nil
	}}))
}

func TestOfferPartsAcceptedCleanupFailure(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	failure := errors.New("old offer dependency cleanup")
	releases := 0
	dep := persistedListTestResult(t, ctx, c, srv, "dep", &transferTestValue{Text: "dep", release: func(context.Context) error { releases++; return failure }})
	offer := testLiveOffer()
	offer.Owner.DependencyIDs = []uint64{uint64(dep.cacheSharedResult().id)}
	out, err := c.OfferParts(ctx, r, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err = c.removePersistedEdge(ctx, dep.cacheSharedResult().id)
	require.NoError(t, err)
	replacement := testLiveOffer()
	replacement.Value.Path = "/next"
	out, err = c.OfferParts(ctx, r, []PersistedPartOffer{replacement})
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, out[0].Err, failure)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.True(t, out[0].Replaced)
	require.Equal(t, 1, releases)
	require.Len(t, c.offerOwners, 1)
	key, _ := partAddressKey(offer.Address)
	require.Equal(t, "/next", r.cacheSharedResult().partOffers[key].record.Value.Path)
	require.Equal(t, int64(1), r.cacheSharedResult().partOffers[key].owner.holds)
}
