package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Entry numbers survive JSON unchanged, zeros and decreases included, and
// numbers close together pack into a byte or two each.
func TestEntryNumbersRoundTrip(t *testing.T) {
	t.Parallel()
	for _, numbers := range []EntryNumbers{
		nil,
		{},
		{7},
		{100, 101, 102, 0, 103, 50, 1 << 40, 3},
	} {
		data, err := json.Marshal(numbers)
		require.NoError(t, err)
		var back EntryNumbers
		require.NoError(t, json.Unmarshal(data, &back))
		require.Equal(t, len(numbers), len(back))
		if len(numbers) > 0 {
			require.Equal(t, numbers, back)
		}
	}

	consecutive := make(EntryNumbers, 60_000)
	for i := range consecutive {
		consecutive[i] = uint64(500_000 + i)
	}
	data, err := json.Marshal(consecutive)
	require.NoError(t, err)
	require.Less(t, len(data), 2*len(consecutive), "consecutive numbers pack into about a byte each")
}

// A packed list that isn't valid is refused, not read short.
func TestEntryNumbersRefusesATruncatedList(t *testing.T) {
	t.Parallel()
	var numbers EntryNumbers
	require.Error(t, json.Unmarshal([]byte(`"gA=="`), &numbers), "a varint cut off")
	require.Error(t, json.Unmarshal([]byte(`"not base64!"`), &numbers))
}
