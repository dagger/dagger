package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedRecordsInTransferForm checks every stored record of a blob-backed
// cache against its normalized transfer form (normalizeTransferRecord): a
// record the Cloud stored from an engine's export is already in that form, so
// normalizing it changes nothing but its offers, which a transfer record
// never carries.
func storedRecordsInTransferForm(t *testing.T, cloud *Cache) int {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	checked := 0
	for id, res := range cloud.resultsByID {
		if res.persistedEnvelope == nil {
			continue
		}
		stored := storedTestRecordLocked(t, res)
		stored.Envelope.PendingOffers = nil
		normalized, err := normalizeTransferRecord(stored)
		require.NoError(t, err, "entry %d", id)
		normalized.Envelope.PendingOffers = nil
		want, err := json.Marshal(stored)
		require.NoError(t, err)
		got, err := json.Marshal(normalized)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(got), "entry %d normalizes to another form", id)
		checked++
	}
	return checked
}

// A record the Cloud stores from an engine's export is already in transfer
// form: normalizing it again changes nothing, so the Cloud can send its
// stored record as it is. Here a root over a dependency, with a part, and a
// call kept on a receiver.
func TestStoredRecordsAreInTransferForm(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, 2, storedRecordsInTransferForm(t, cloud))

	test := newReceiverCallTest(t)
	require.Equal(t, 2, storedRecordsInTransferForm(t, test.cloud))
}

// sendAsStoredTestEngine is an engine with a root R whose payload names its
// dependency P, made after unrelated entries, so that no entry's number is
// its place in a bundle.
func sendAsStoredTestEngine(t *testing.T) (context.Context, *Cache, AnyResult, AnyResult) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	for i := range 5 {
		persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("as-stored-padding-%d", i), String("unrelated"))
	}
	p := persistedListTestResult(t, ctx, c, srv, "as-stored-dep", &transferTestValue{Text: "dep"})
	r := persistedListTestResult(t, ctx, c, srv, "as-stored-root", &transferTestValue{Text: "root", Child: rowNumber(p)})
	transferTestDependency(c, ctx, r, p)
	return ctx, c, r, p
}

// sentChild is the entry number a transferTestValue record names as its
// child, as the bundle's encoding carries it.
func sentChild(t *testing.T, sent TransferredValue) uint64 {
	t.Helper()
	var value transferTestValue
	require.NoError(t, json.Unmarshal(sentRecord(t, sent).Envelope.ObjectJSON, &value))
	return value.Child
}

// sentRecord is a bundle value's record as the bundle's encoding carries it.
func sentRecord(t *testing.T, value TransferredValue) PersistedRecord {
	t.Helper()
	var decoded TransferredValue
	require.NoError(t, json.Unmarshal(mustTransferJSON(t, value), &decoded))
	return decoded.Record
}

// requireNamesItself checks that a sent record names itself by its sender
// number, in the record and in its root envelope.
func requireNamesItself(t *testing.T, value TransferredValue) {
	t.Helper()
	require.Equal(t, value.SenderNumber, value.Record.ResultID, "row %d names itself by its entry's number", value.Ordinal)
	if value.Record.Envelope.ResultID != 0 {
		require.Equal(t, value.SenderNumber, value.Record.Envelope.ResultID, "row %d's envelope names its entry's number", value.Ordinal)
	}
}

// An engine's export carries each record as the engine holds it: naming
// itself and the records it references by the engine's own entry numbers,
// not by places in the bundle.
func TestExportSendsRecordsAsTheEngineHoldsThem(t *testing.T) {
	t.Parallel()
	ctx, c, r, p := sendAsStoredTestEngine(t)
	bundle := exportTestBundle(t, ctx, c, r)
	require.Len(t, bundle.Values, 2)
	for _, value := range bundle.Values {
		requireNamesItself(t, value)
		if value.SenderNumber == rowNumber(r) {
			require.Equal(t, rowNumber(p), sentChild(t, value), "R names P by P's entry number")
		}
	}
}

// The Cloud sends each stored record exactly as it stores it, in transfer
// form: naming itself and the records it references by the Cloud's entry
// numbers, with nothing rewritten.
func TestStoredBundleSendsRecordsAsStored(t *testing.T) {
	t.Parallel()
	ctx, c, r, p := sendAsStoredTestEngine(t)
	exported := exportTestBundle(t, ctx, c, r)
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	// Records merged first take the Cloud's first numbers.
	for i := range 3 {
		padding, _ := mergeTestSource(t, fmt.Sprintf("as-stored-cloud-padding-%d", i), "unrelated")
		_, err := cloud.MergeValues(cloudCtx, "cache-x", padding)
		require.NoError(t, err)
	}
	reply, err := cloud.MergeValues(cloudCtx, "cache-a", exported)
	require.NoError(t, err)
	numbers := map[uint64]uint64{}
	for _, value := range reply.Values {
		for _, sent := range exported.Values {
			if sent.Ordinal == value.Ordinal {
				numbers[sent.SenderNumber] = value.Number
			}
		}
	}
	rCloud, pCloud := numbers[rowNumber(r)], numbers[rowNumber(p)]
	require.NotZero(t, rCloud)
	require.NotZero(t, pCloud)

	got, err := cloud.StoredBundle(cloudCtx, []uint64{rCloud}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, 2)
	for _, value := range got.Bundle.Values {
		requireNamesItself(t, value)
		require.JSONEq(t, string(mustTransferJSON(t, storedTestTransferRecord(t, cloud, value.SenderNumber))), string(mustTransferJSON(t, sentRecord(t, value))), "entry %d goes as stored", value.SenderNumber)
		if value.SenderNumber == rCloud {
			require.Equal(t, pCloud, sentChild(t, value), "R names P by the Cloud's number")
		}
	}
}

// storedTestTransferRecord is the Cloud entry's stored record in transfer
// form, without offers.
func storedTestTransferRecord(t *testing.T, cloud *Cache, number uint64) PersistedRecord {
	t.Helper()
	cloud.egraphMu.RLock()
	res := cloud.resultsByID[sharedResultID(number)]
	require.NotNil(t, res)
	stored := storedTestRecordLocked(t, res)
	cloud.egraphMu.RUnlock()
	rec, err := normalizeTransferRecord(stored)
	require.NoError(t, err)
	rec.Envelope.PendingOffers = nil
	return rec
}

// A record that names a number no row of its bundle has is refused, and the
// merge changes nothing.
func TestMergeValuesRefusesAReferenceOutsideTheBundle(t *testing.T) {
	t.Parallel()
	ctx, c, r, _ := sendAsStoredTestEngine(t)
	bundle := exportTestBundle(t, ctx, c, r)
	var broken ValueBundle
	require.NoError(t, json.Unmarshal(mustTransferJSON(t, bundle), &broken))
	for i := range broken.Values {
		if broken.Values[i].SenderNumber != rowNumber(r) {
			continue
		}
		payload, err := json.Marshal(&transferTestValue{Text: "root", Child: 424242})
		require.NoError(t, err)
		broken.Values[i].Record.Envelope.ObjectJSON = payload
	}
	bctx, b, _ := transferTestCache(t)
	b.egraphMu.RLock()
	before := len(b.resultsByID)
	b.egraphMu.RUnlock()
	_, err := b.MergeValues(bctx, cloudCacheID, broken)
	require.ErrorContains(t, err, "424242")
	b.egraphMu.RLock()
	defer b.egraphMu.RUnlock()
	require.Len(t, b.resultsByID, before)
}
