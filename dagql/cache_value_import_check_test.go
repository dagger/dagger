package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// checkTestBundle is a stored bundle as a blob-backed cache builds it: a
// root record carried compressed, over a held row, which comes first, with
// an output at a part the root's record declares pending; and the root's
// declared parts.
func checkTestBundle(t *testing.T) (ValueBundle, map[uint64][]declaredPart) {
	t.Helper()
	address := PersistedPartAddress{OutputPath: PersistedRefPath{}.Field("fs"), Part: "snapshot"}
	key, err := partAddressKey(address)
	require.NoError(t, err)
	value := SnapshotValue{Kind: "directory", Path: "/"}
	frame := &ResultCall{Kind: ResultCallKindField, Field: "checked", Type: NewResultCallType((&transferTestValue{}).Type())}
	bundle := ValueBundle{
		Version: ValueBundleVersion,
		Roots:   []TransferredRoot{{Ordinal: 1}},
		Values: []TransferredValue{
			{Ordinal: 2, SenderNumber: 8, Record: PersistedRecord{ResultID: 8}, Held: "xxh3:0123456789abcdef"},
			{Ordinal: 1, SenderNumber: 7, Record: PersistedRecord{ResultID: 7, Call: frame}, DependencyIDs: []uint64{2}},
		},
		Outputs: []TransferredOutput{{Ordinal: 1, Address: address, State: "completed", Value: &value, Chain: &OfferedChain{}, Owner: &PersistedOfferOwner{}}},
	}
	stored := map[uint64][]declaredPart{1: {{key: key, address: address, state: "pending", kind: "directory"}}}
	return bundle, stored
}

// A stored bundle's check finds each fault it is given, one at a time, and
// passes the bundle without it: its rows by ordinal, its calls' and its
// outputs' services' rows, a compressed record's declared parts, where the
// last part declared at an address is the one an output is checked
// against, and its closure.
func TestAStoredBundlesCheckFindsEachFault(t *testing.T) {
	t.Parallel()
	bundle, stored := checkTestBundle(t)
	require.NoError(t, validateStoredBundle(bundle, stored), "the bundle as built")

	for _, tc := range []struct {
		name  string
		fault func(*ValueBundle, map[uint64][]declaredPart)
		err   string
	}{
		{"an ordinal past the rows", func(b *ValueBundle, _ map[uint64][]declaredPart) { b.Values[1].Ordinal = 3 }, "invalid ordinal 3"},
		{"two rows of one ordinal", func(b *ValueBundle, _ map[uint64][]declaredPart) { b.Values[1].Ordinal = 2 }, "duplicate ordinal 2"},
		{"a call on no row", func(b *ValueBundle, _ map[uint64][]declaredPart) {
			b.Calls = []TransferredCall{{Call: "call", Value: 5}}
		}, "call 0: value 5 is no row"},
		{"an output's service on no row", func(b *ValueBundle, _ map[uint64][]declaredPart) {
			value := *b.Outputs[0].Value
			value.Services = []TransferredServiceBinding{{ServiceResultID: 9}}
			b.Outputs[0].Value = &value
			b.Outputs[0].Owner = &PersistedOfferOwner{DependencyIDs: []uint64{9}}
		}, "output refers to missing service 9"},
		{"an output at a part the record doesn't declare", func(_ *ValueBundle, s map[uint64][]declaredPart) {
			part := s[1][0]
			part.key = "other"
			s[1] = []declaredPart{part}
		}, "unknown output address"},
		{"a part declared absent after pending", func(_ *ValueBundle, s map[uint64][]declaredPart) {
			absent := s[1][0]
			absent.state = "absent"
			s[1] = []declaredPart{s[1][0], absent}
		}, "output state differs from recorded part"},
		{"a row outside the roots' closure", func(b *ValueBundle, _ map[uint64][]declaredPart) {
			b.Values = append(b.Values, TransferredValue{Ordinal: 3, SenderNumber: 9, Record: PersistedRecord{ResultID: 9}, Held: "xxh3:fedcba9876543210"})
		}, "outside its root closure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bundle, stored := checkTestBundle(t)
			tc.fault(&bundle, stored)
			require.ErrorContains(t, validateStoredBundle(bundle, stored), tc.err)
		})
	}

	// Declared pending after absent, the part is pending, as the output
	// needs.
	bundle, stored = checkTestBundle(t)
	absent := stored[1][0]
	absent.state = "absent"
	stored[1] = []declaredPart{absent, stored[1][0]}
	require.NoError(t, validateStoredBundle(bundle, stored), "the last part declared at the address")
}
