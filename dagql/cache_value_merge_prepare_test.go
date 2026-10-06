package dagql

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeTestBadPayload gives a bundle's only record a payload its codec
// refuses, in an envelope that is well formed.
func mergeTestBadPayload(t *testing.T, bundle ValueBundle) {
	t.Helper()
	require.Len(t, bundle.Values, 1)
	bundle.Values[0].Record.Envelope.ObjectJSON = json.RawMessage(`{"text":5}`)
}

// A record whose recipe's entry holds a value merge keeps is prepared from
// its call and offers only: its payload isn't decoded, so one its codec would
// refuse is merged, and the entry keeps its value. The same record is refused
// where merge would install it.
func TestMergeValuesKeptRecordSkipsItsPayload(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	mergeTestBadPayload(t, bundle)

	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
	b.egraphMu.RLock()
	payloadBytes := local.cacheSharedResult().payloadBytes
	b.egraphMu.RUnlock()
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Equal(t, uint64(local.cacheSharedResult().id), reply.Imported()[0].ResultID)
	b.egraphMu.RLock()
	imported := local.cacheSharedResult().imported
	require.Equal(t, payloadBytes, local.cacheSharedResult().payloadBytes, "the entry counts its own value")
	b.egraphMu.RUnlock()
	require.False(t, imported, "the entry keeps its own value")
	total, sum := cacheTestPayloadTotalConsistent(b)
	require.Equal(t, sum, total)

	ectx, empty, _ := transferTestCache(t)
	_, err = empty.MergeValues(ectx, cloudCacheID, bundle)
	require.ErrorContains(t, err, "codec dagql_test.Transfer")
	empty.egraphMu.RLock()
	defer empty.egraphMu.RUnlock()
	require.Empty(t, empty.resultsByID, "an installed record's payload is validated")
}

// A kept record's shape is still checked: a call that names a row it doesn't
// depend on is refused, whatever the cache holds.
func TestMergeValuesKeptRecordChecksItsShape(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
	row := &bundle.Values[0]
	row.Record.Call.Receiver = &ResultCallRef{ResultID: uint64(row.Ordinal)}
	_, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.ErrorContains(t, err, "not a direct dependency")
}

// A record prepared as kept that the decision installs after all, since the
// entry's value expired in between, sends the merge back to a full
// preparation: its payload is validated, and the record it installs is the
// bundle's.
func TestMergeValuesInstallingAKeptRecordPreparesInFull(t *testing.T) {
	t.Parallel()
	for _, bad := range []bool{false, true} {
		name := "valid"
		if bad {
			name = "refused-payload"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bundle, _ := mergeTestSource(t, "root", "sent")
			if bad {
				mergeTestBadPayload(t, bundle)
			}
			bctx, b, bsrv := transferTestCache(t)
			local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
			prepared := 0
			b.testTransferPlanPrepared = func(i int) error {
				if i == 1 {
					prepared++
				}
				return nil
			}
			b.testBeforeTransferCommit = func() { mergeTestExpire(b, local.cacheSharedResult()) }
			reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
			require.Equal(t, 2, prepared, "prepared light, then in full")
			if bad {
				require.ErrorContains(t, err, "codec dagql_test.Transfer")
				return
			}
			require.NoError(t, err)
			require.True(t, reply.Committed)
			b.egraphMu.RLock()
			res := b.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
			require.NotNil(t, res)
			imported, envelope, payloadBytes := res.imported, res.persistedEnvelope, res.payloadBytes
			b.egraphMu.RUnlock()
			require.True(t, imported, "the record replaced the expired value")
			require.JSONEq(t, `{"text":"sent"}`, string(envelope.ObjectJSON))
			require.Equal(t, persistedEnvelopePayloadBytes(envelope), payloadBytes, "the entry counts the installed record's envelope")
			total, sum := cacheTestPayloadTotalConsistent(b)
			require.Equal(t, sum, total)
		})
	}
}

// A call that names a row it doesn't depend on is refused before merge
// derives any identity from it: a call naming its own row would otherwise
// wait on its own recipe forever.
func TestMergeValuesRefusesUndeclaredCallReferenceBeforeIdentity(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "self-call", "value")
	require.Len(t, bundle.Values, 1)
	row := &bundle.Values[0]
	row.Record.Call.Receiver = &ResultCallRef{ResultID: uint64(row.Ordinal)}
	ctx, cache, _ := transferTestCache(t)
	_, err := cache.MergeValues(ctx, cloudCacheID, bundle)
	require.ErrorContains(t, err, "not a direct dependency")
}

// A kept record whose entry expires before the decision is prepared in full
// before offers are selected and the graph is checked. A complete best record
// and an offered duplicate of its recipe coalesce: the full preparation knows
// the installed part is complete, so the duplicate's offer, whose owner would
// close a cycle, is discarded.
func TestMergeValuesFullPreparationBeforeOfferGraph(t *testing.T) {
	t.Parallel()
	initial, _ := mergeTestSource(t, "fallback-x", "ready")
	ctx, c, _ := transferTestCache(t)
	reply, err := c.MergeValues(ctx, cloudCacheID, initial)
	require.NoError(t, err)
	target := c.resultsByID[sharedResultID(reply.Values[0].Number)]
	x := initial.Values[0]
	x.Ordinal = 1
	x.SenderNumber = 1
	x.Record.ResultID = 1
	x.Record.Envelope.ResultID = 1
	x2 := x
	x2.Ordinal = 2
	x2.SenderNumber = 2
	x2.Record.ResultID = 2
	x2.Record.Envelope.ResultID = 2
	x2.Record.Envelope.ObjectJSON = json.RawMessage(`{"text":"pending"}`)
	offer := testLiveOffer()
	offer.Owner.DependencyIDs = []uint64{3}
	x2.Record.Envelope.PendingOffers = []PersistedPartOffer{offer}
	frame := &ResultCall{Kind: ResultCallKindField, Field: "fallback-y", Type: NewResultCallType(String("").Type())}
	y := TransferredValue{Ordinal: 3, SenderNumber: 3, DependencyIDs: []uint64{1}, Record: PersistedRecord{ResultID: 3, Call: frame, Envelope: PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, ResultID: 3, TypeName: "String", ScalarJSON: json.RawMessage(`"y"`)}}}
	bundle := ValueBundle{Version: valueBundleVersion, Roots: []TransferredRoot{{Ordinal: 2}}, Values: []TransferredValue{x, x2, y}}
	_, err = validateValueBundle(bundle)
	require.NoError(t, err)
	c.testBeforeTransferCommit = func() { mergeTestExpire(c, target) }
	reply, err = c.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
}

// The Cloud drops the bundle's offers and outputs, and with them their
// owners' edges: a row reachable only through such an edge fails the roots'
// closure check after the drop, before anything is published. An engine,
// which keeps the offer, accepts the same bundle.
func TestMergeValuesCloudChecksClosureAfterDroppingParts(t *testing.T) {
	t.Parallel()
	for _, output := range []bool{false, true} {
		name := "pending-offer"
		if output {
			name = "transferred-output"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, source, srv := transferTestCache(t)
			owner := persistedListTestResult(t, ctx, source, srv, "offer-owner", String("owner"))
			root := persistedListTestResult(t, ctx, source, srv, "offered-root", &transferTestValue{Text: "pending"})
			mergeTestOfferOn(t, ctx, source, root, "/offered", owner)
			bundle := exportTestBundle(t, ctx, source, root)
			require.Len(t, bundle.Values, 2)
			if output {
				for i := range bundle.Values {
					row := &bundle.Values[i]
					for _, offer := range row.Record.Envelope.PendingOffers {
						bundle.Outputs = append(bundle.Outputs, TransferredOutput{Ordinal: row.Ordinal, Address: offer.Address, State: "completed", Value: &offer.Value, Chain: &offer.Chain, Owner: &offer.Owner})
					}
					row.Record.Envelope.PendingOffers = nil
				}
				require.Len(t, bundle.Outputs, 1)
			}
			_, err := validateValueBundle(bundle)
			require.NoError(t, err)

			ectx, engine, _ := transferTestCache(t)
			reply, err := engine.MergeValues(ectx, cloudCacheID, bundle)
			require.NoError(t, err)
			require.True(t, reply.Committed)

			cctx, cloud := storedPartTestCache(t, WithBlobStore())
			_, err = cloud.MergeValues(cctx, "sender", bundle)
			require.ErrorContains(t, err, "outside its root closure")
			require.Empty(t, cloud.resultsByID, "nothing is published")
		})
	}
}
