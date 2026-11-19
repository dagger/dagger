package dagql

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedPartsTestWant is what describing each value's record finds, by its
// sender number: the parts it proves complete and those it declares absent
// or pending, as a bundle carries them.
func storedPartsTestWant(t *testing.T, bundle ValueBundle) map[uint64]*TransferredRecordParts {
	t.Helper()
	decoded, err := cloneValueBundle(bundle)
	require.NoError(t, err)
	want := map[uint64]*TransferredRecordParts{}
	for _, value := range decoded.Values {
		if value.Held != "" {
			continue
		}
		memos := payloadMemos{}
		outputs, err := mapMemoTransferredOutputs(value.Record, memos)
		require.NoError(t, err)
		declared, err := declaredPartsOf(outputs)
		require.NoError(t, err)
		want[value.SenderNumber] = transferredRecordParts(declared, sharedPartKeys(memoRecordCompletePartKeys(value.Record, memos)))
	}
	return want
}

// storedPartsTestCheck requires that each value of the Cloud's bundle of
// roots carries the parts describing its record finds, and returns how many
// carry a complete part.
func storedPartsTestCheck(t *testing.T, cloud *Cache, roots []uint64) int {
	t.Helper()
	got, err := cloud.StoredBundle(t.Context(), roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	want := storedPartsTestWant(t, got.Bundle)
	complete := 0
	for _, value := range got.Bundle.Values {
		if value.Held != "" {
			continue
		}
		require.NotNil(t, value.Parts, "entry %d carries its parts", value.SenderNumber)
		require.Equal(t, want[value.SenderNumber], value.Parts, "entry %d carries the parts its record describes", value.SenderNumber)
		if len(value.Parts.Complete) > 0 {
			complete++
		}
	}
	return complete
}

// A bundle the Cloud sends carries, on each record, the parts the record
// proves complete and those it declares absent or pending, as describing the
// record finds them: from a record held whole or compressed, and from one
// the Cloud restored from its save. An engine reads them there instead of
// decoding the record's payload.
func TestStoredBundleCarriesEachRecordsParts(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	ready := persistedListTestResult(t, actx, a, asrv, "parts-ready", &transferTestValue{Text: "ready"})
	lazy := persistedListTestResult(t, actx, a, asrv, "parts-lazy", &transferTestValue{Text: "lazy"})
	transferTestDependency(a, actx, ready, lazy)
	bundle := exportTestBundle(t, actx, a, ready)

	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	storedTestHoldWhole(cloud)
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	require.Len(t, roots, 1)
	require.Equal(t, 1, storedPartsTestCheck(t, cloud, roots), "the ready record carries its complete part, held whole")

	storedTestCompress(cloud)
	require.Equal(t, 1, storedPartsTestCheck(t, cloud, roots), "and held compressed")
	require.NoError(t, cloud.Close(ctx))

	ctx, restored := storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, restored.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetNone, restored.PersistenceResetReason())
	require.Equal(t, 1, storedPartsTestCheck(t, restored, roots), "and restored from its save")
}
