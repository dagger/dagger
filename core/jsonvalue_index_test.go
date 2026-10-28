package core

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestIndexedJSONValueConcurrentLookupAndPersistence(t *testing.T) {
	data := []byte(`{"packages":{"dir/with/slash":{"names":["TestA","TestB"]}},"null":null}`)
	value := NewIndexedJSONValue(data)
	require.Greater(t, value.CachePayloadBytes(), int64(len(data)))
	data[0] = '['
	require.Equal(t, byte('{'), value.Data[0], "the value owns its input bytes")
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got, err := value.FieldAt([]string{"packages", "dir/with/slash", "names"})
			if err != nil {
				t.Error(err)
				return
			}
			require.JSONEq(t, `["TestA","TestB"]`, string(got.Data))
		})
	}
	wg.Wait()
	_, err := value.FieldAt([]string{"missing"})
	require.EqualError(t, err, "no such field: 'missing'")
	_, err = value.FieldAt([]string{"null", "missing"})
	require.EqualError(t, err, "can't lookup field 'missing' in non-object value")
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "decoded")
	decoded, err := (*JSONValue)(nil).DecodePersistedObject(t.Context(), (*dagql.PersistDecodeContext)(nil), encoded)
	require.NoError(t, err)
	restored := decoded.(*JSONValue)
	require.NotNil(t, restored.decoded)
	got, err := restored.FieldAt([]string{"packages", "dir/with/slash", "names"})
	require.NoError(t, err)
	require.JSONEq(t, `["TestA","TestB"]`, string(got.Data))
}

func TestIndexedJSONValueKeepsContentsForLargeNumbers(t *testing.T) {
	value := NewIndexedJSONValue([]byte(`{"large":1e500}`))
	require.Equal(t, `{"large":1e500}`, string(value.Data))
	_, err := value.FieldAt([]string{"large"})
	require.Error(t, err, "field decoding preserves the previous float64 range error")
}
