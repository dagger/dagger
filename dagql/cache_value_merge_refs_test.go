package dagql

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// An engine prepares each record of a bundle the Cloud stored as validating
// it in full and then relocating every reference it makes, its offers'
// included, would: one visit of its references checks and relocates them.
func TestMergePreparationRelocatesAsValidateThenVisit(t *testing.T) {
	t.Parallel()
	cctx, cloud, batch := storedBenchCloud(t)
	got, err := cloud.StoredBundle(cctx, batch, time.Now().Unix(), nil)
	require.NoError(t, err)
	// Each part is owned by its row's dependencies, as an engine that
	// captured it from its row's inputs offers it.
	deps := map[TransferOrdinal][]uint64{}
	for _, row := range got.Bundle.Values {
		deps[row.Ordinal] = row.DependencyIDs
	}
	for i := range got.Bundle.Outputs {
		output := &got.Bundle.Outputs[i]
		if output.Chain != nil {
			output.Owner = &PersistedOfferOwner{DependencyIDs: slices.Clone(deps[output.Ordinal])}
		}
	}

	ctx, engine, _ := transferTestCache(t)
	// The test reads each row's record as the bundle carried it, from the
	// prepared merge: the preparation keeps its copies of them.
	engine.testKeepRecordCopies = true
	// Provisional numbers start past the ordinals, so that relocating
	// changes every reference.
	engine.egraphMu.Lock()
	engine.nextSharedResultID = 10_000
	engine.egraphMu.Unlock()
	prep, err := engine.prepareValueMerge(ctx, cloudCacheID, got.Bundle, false)
	require.NoError(t, err)
	require.Empty(t, prep.stale)
	require.Equal(t, uint64(10_000), prep.firstID)

	relocate := func(ref *PersistedRef) {
		if ref.RecipeID == nil && ref.ResultID != 0 {
			ref.ResultID = prep.firstID + ref.ResultID - 1
		}
	}
	offers, owned, children := 0, 0, 0
	for _, id := range prep.order {
		row := prep.index[id]
		require.NoError(t, validateTransferRecord(row.Record, row.DependencyIDs))
		var refs []uint64
		want, err := VisitEncodedReferences(row.Record, func(ref *PersistedRef) error {
			if ref.Kind == PersistedRefChild && ref.ResultID != 0 {
				children++
			}
			relocate(ref)
			if ref.RecipeID == nil && ref.ResultID != 0 {
				refs = append(refs, ref.ResultID)
			}
			return nil
		})
		require.NoError(t, err)
		want.Envelope.Imported = true
		want.Call = prep.records[id].Call
		offers += len(want.Envelope.PendingOffers)
		for _, offer := range want.Envelope.PendingOffers {
			owned += len(offer.Owner.DependencyIDs)
		}
		slices.Sort(refs)

		gotJSON, err := json.Marshal(prep.records[id])
		require.NoError(t, err)
		wantJSON, err := json.Marshal(want)
		require.NoError(t, err)
		require.JSONEq(t, string(wantJSON), string(gotJSON), "row %d", id)
		require.Equal(t, slices.Compact(refs), prep.refs[id], "row %d", id)
	}
	require.NotZero(t, offers, "the bundle carries offers")
	require.NotZero(t, owned, "the offers reference rows")
	require.NotZero(t, children, "the payloads reference rows")
}

// A payload reference to a row the record doesn't depend on is refused, as
// a call reference is.
func TestMergeValuesRefusesUndeclaredPayloadReference(t *testing.T) {
	t.Parallel()
	cctx, cloud, batch := storedBenchCloud(t)
	got, err := cloud.StoredBundle(cctx, batch[:1], time.Now().Unix(), nil)
	require.NoError(t, err)
	// The bundle as its encoding carries it.
	bundle, err := cloneValueBundle(got.Bundle)
	require.NoError(t, err)
	var target *TransferredValue
	for i := range bundle.Values {
		if len(bundle.Values[i].DependencyIDs) == 1 {
			target = &bundle.Values[i]
			break
		}
	}
	require.NotNil(t, target)
	var other TransferOrdinal
	for _, row := range bundle.Values {
		if row.Ordinal != target.Ordinal && uint64(row.Ordinal) != target.DependencyIDs[0] {
			other = row.Ordinal
			break
		}
	}
	require.NotZero(t, other)
	value, err := readStoredBenchPayload(target.Record.Envelope.ObjectJSON)
	require.NoError(t, err)
	require.Equal(t, target.DependencyIDs[0], value.Child)
	value.Child = uint64(other)
	target.Record.Envelope.ObjectJSON, err = json.Marshal(value)
	require.NoError(t, err)

	ctx, engine, _ := transferTestCache(t)
	_, err = engine.MergeValues(ctx, cloudCacheID, bundle)
	require.ErrorContains(t, err, "not a direct dependency")
	engine.egraphMu.RLock()
	defer engine.egraphMu.RUnlock()
	require.Empty(t, engine.resultsByID)
}
