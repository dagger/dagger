package dagql

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A value encodes to the same JSON whether its holder's encoder writes it in
// place or copies MarshalJSON's bytes: a record a blob-backed cache holds
// compressed, and one held decoded, as a held row and with its
// dependencies, alone and inside a bundle.
func TestTransferredValueEncodesTheSameInPlace(t *testing.T) {
	t.Parallel()
	bundle := storedBodyTestExport(t)
	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	// The Cloud compresses a record within seconds of its first use.
	var stored ValueBundle
	require.Eventually(t, func() bool {
		stored = storedBodyTestUse(t, ctx, cloud, roots)
		return slices.ContainsFunc(stored.Values, func(v TransferredValue) bool { return v.storedEnvelope != nil })
	}, time.Minute, 100*time.Millisecond, "a record held compressed")

	values := append([]TransferredValue{}, stored.Values...)
	values = append(values, bundle.Values...)
	held := bundle.Values[0]
	held.Record = PersistedRecord{ResultID: held.SenderNumber}
	held.Held = testDigest("held")
	values = append(values, held)
	for i, v := range values {
		want, err := v.MarshalJSON()
		require.NoError(t, err, "value %d", i)
		got, err := json.Marshal(v)
		require.NoError(t, err, "value %d", i)
		require.Equal(t, string(want), string(got), "value %d", i)
	}
	for _, b := range []ValueBundle{stored, bundle} {
		got, err := json.Marshal(b)
		require.NoError(t, err)
		var copied []json.RawMessage
		for _, v := range b.Values {
			raw, err := v.MarshalJSON()
			require.NoError(t, err)
			copied = append(copied, raw)
		}
		var want map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(got, &want))
		gotValues, err := json.Marshal(copied)
		require.NoError(t, err)
		require.Equal(t, string(gotValues), string(want["values"]))
	}
}
