package protocol

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
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
	require.Error(t, json.Unmarshal([]byte(`"/////////////wE="`), &numbers), "a varint too large for 64 bits")
}

// A restored file's complete parts pack sparsely, each distinct address
// once, and unpack to the same parts at the same positions.
func TestCompletePartsRoundTrip(t *testing.T) {
	t.Parallel()
	meta := dagql.PersistedPartAddress{Part: "metadata"}
	exec := dagql.PersistedPartAddress{Part: "execMeta"}
	nested := dagql.PersistedPartAddress{OutputPath: dagql.PersistedRefPath{}.Field("rootfs"), Part: "fs"}
	parts := [][]dagql.PersistedPartAddress{nil, {meta, exec}, nil, nil, {meta}, {nested, meta}, nil}
	table, packed := PackCompleteParts(parts)
	require.Len(t, table, 3, "each distinct address once")
	got, err := UnpackCompleteParts(table, packed)
	require.NoError(t, err)
	require.Equal(t, map[int][]dagql.PersistedPartAddress{1: {meta, exec}, 4: {meta}, 5: {nested, meta}}, got)

	table, packed = PackCompleteParts(make([][]dagql.PersistedPartAddress, 5))
	require.Empty(t, table)
	require.Empty(t, packed, "no complete part takes no bytes")
	got, err = UnpackCompleteParts(table, packed)
	require.NoError(t, err)
	require.Empty(t, got)
}

// Packed parts that name a part the table lacks, or end mid-record, are
// refused.
func TestCompletePartsRefusesAMalformedList(t *testing.T) {
	t.Parallel()
	table := []dagql.PersistedPartAddress{{Part: "metadata"}}
	_, err := UnpackCompleteParts(table, []byte{0, 1, 1})
	require.Error(t, err, "index 1 of a table of 1")
	_, err = UnpackCompleteParts(table, []byte{0, 2, 0})
	require.Error(t, err, "two parts announced, one given")
	huge := binary.AppendUvarint([]byte{0}, math.MaxUint64)
	_, err = UnpackCompleteParts(table, huge)
	require.Error(t, err, "a count larger than what follows is refused before anything is made for it")
}

// Complete parts read by position name, at every position, the parts
// UnpackCompleteParts gives it, by their indexes in the table.
func TestPartIndexesMatchCompleteParts(t *testing.T) {
	t.Parallel()
	meta := dagql.PersistedPartAddress{Part: "metadata"}
	exec := dagql.PersistedPartAddress{Part: "execMeta"}
	nested := dagql.PersistedPartAddress{OutputPath: dagql.PersistedRefPath{}.Field("rootfs"), Part: "fs"}
	parts := [][]dagql.PersistedPartAddress{{exec}, {meta, exec}, nil, nil, {meta}, {nested, meta}, nil, nil}
	table, packed := PackCompleteParts(parts)
	x, err := UnpackPartIndexes(packed, len(table), len(parts))
	require.NoError(t, err)
	for p := range parts {
		var got []dagql.PersistedPartAddress
		for _, i := range x.At(p) {
			got = append(got, table[i])
		}
		require.Equal(t, parts[p], got, "position %d", p)
	}
	require.Empty(t, x.At(len(parts)), "past the last position")

	x, err = UnpackPartIndexes(nil, 0, 5)
	require.NoError(t, err)
	require.Empty(t, x.At(2), "no complete part, none at any position")
}

// Packed parts read by position refuse what UnpackCompleteParts refuses,
// and a position past the file's entries or out of order.
func TestPartIndexesRefuseAMalformedList(t *testing.T) {
	t.Parallel()
	_, err := UnpackPartIndexes([]byte{0, 1, 1}, 1, 3)
	require.Error(t, err, "index 1 of a table of 1")
	_, err = UnpackPartIndexes([]byte{0, 2, 0}, 1, 3)
	require.Error(t, err, "two parts announced, one given")
	_, err = UnpackPartIndexes([]byte{3, 1, 0}, 1, 3)
	require.Error(t, err, "position 3 of 3 entries")
	_, err = UnpackPartIndexes([]byte{1, 1, 0, 0, 1, 0}, 1, 3)
	require.Error(t, err, "position 1 twice")
}
