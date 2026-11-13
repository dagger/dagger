package protocol

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// allocatedBy returns the bytes the heap allocated running f, on average
// over n runs.
func allocatedBy(n int, f func()) float64 {
	f()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range n {
		f()
	}
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(n)
}

// EncodeEnvelope gives the bytes json.Marshal gives for NewEnvelope's
// envelope, for a merge and for other bodies, its error as NewEnvelope's.
func TestEncodeEnvelopeIsTheEnvelopesJSON(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		re   uint64
		typ  Type
		body any
	}{
		"merge":   {0, TypeMerge, sharedLayersMerge()},
		"records": {0, TypeMerge, recordsMerge()},
		"lookup":  {0, TypeMerge, Merge{Bundle: dagql.ValueBundle{Version: 8}, Lookup: 7}},
		"reply":   {41, TypeError, Error{Message: "<no & such>"}},
		"hello":   {0, TypeHello, Hello{CacheID: "cache-a", Generation: 2}},
	} {
		env, err := NewEnvelope(9, c.re, c.typ, c.body)
		require.NoError(t, err, name)
		want, err := json.Marshal(env)
		require.NoError(t, err, name)
		got, err := EncodeEnvelope(9, c.re, c.typ, c.body)
		require.NoError(t, err, name)
		require.Equal(t, string(want), string(got), name)
	}
	_, want := NewEnvelope(1, 0, TypeMerge, func() {})
	_, got := EncodeEnvelope(1, 0, TypeMerge, func() {})
	require.Error(t, got)
	require.Equal(t, want.Error(), got.Error())
}

// recordsMerge is a merge shaped as a fresh engine's placement merges are:
// 2,000 records, each with its object's JSON and its call, over the record
// before it.
func recordsMerge() Merge {
	object := json.RawMessage(`{"rootfs":{"id":"` + strings.Repeat("a", 64) + `"},"config":{"env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","GOTOOLCHAIN=auto"],"workdir":"/src","entrypoint":["/bin/sh","-c"]},"platform":"linux/arm64","mounts":[]}`)
	bundle := dagql.ValueBundle{Version: 8, Roots: []dagql.TransferredRoot{{Ordinal: 2000}}}
	for i := range 2000 {
		v := dagql.TransferredValue{
			Ordinal:      dagql.TransferOrdinal(i + 1),
			SenderNumber: uint64(1000 + i),
			Record: dagql.PersistedRecord{
				ResultID: uint64(1000 + i),
				Envelope: dagql.PersistedResultEnvelope{Version: 1, Kind: "object", TypeName: "Container", ObjectCodec: "core", ResultID: uint64(1000 + i), ObjectJSON: object},
				Call:     &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "withExec", EffectIDs: []string{fmt.Sprintf("effect-%d", i)}},
			},
			ExpiresAtUnix: 1790086400,
		}
		if i > 0 {
			v.DependencyIDs = []uint64{uint64(i)}
		}
		bundle.Values = append(bundle.Values, v)
	}
	return Merge{Bundle: bundle}
}

// A merge's message is encoded once, in place: what encoding it allocates
// is about the message's own size, not the copies of it each encoder above
// the merge and its values made, checked and copied again: the values' own
// bytes in the merge's, the merge's in the envelope's. Encoded so, a merge
// of records allocated over ten times its message.
func TestMergeMessageIsEncodedInPlace(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates beside the encoding")
	}
	merge := recordsMerge()
	data, err := EncodeEnvelope(1, 0, TypeMerge, merge)
	require.NoError(t, err)
	allocated := allocatedBy(5, func() {
		if _, err := EncodeEnvelope(1, 0, TypeMerge, merge); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.0f bytes allocated for a %d-byte message: %.2f times", allocated, len(data), allocated/float64(len(data)))
	require.Less(t, allocated, 3.5*float64(len(data)))
}
