//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

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
// bytes in the merge's, the merge's in the envelope's. With encoding/json's
// buffer pools empty, as a collection leaves them, a merge of 2,000 records
// allocated 16 to 17 times its message so, and 8.3 times encoded once; the
// race detector doesn't change the count.
func TestMergeMessageIsEncodedInPlace(t *testing.T) {
	merge := recordsMerge()
	data, err := EncodeEnvelope(1, 0, TypeMerge, merge)
	require.NoError(t, err)
	allocated := allocatedBy(5, func() {
		if _, err := EncodeEnvelope(1, 0, TypeMerge, merge); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.0f bytes allocated for a %d-byte message: %.2f times", allocated, len(data), allocated/float64(len(data)))
	require.Less(t, allocated, 12*float64(len(data)))
}

// EncodeEnvelope gives a merge of records the bytes json.Marshal gives for
// NewEnvelope's envelope, its values written in place.
func TestEncodeEnvelopeIsTheEnvelopesJSONForRecords(t *testing.T) {
	t.Parallel()
	merge := recordsMerge()
	env, err := NewEnvelope(9, 0, TypeMerge, merge)
	require.NoError(t, err)
	want, err := json.Marshal(env)
	require.NoError(t, err)
	got, err := EncodeEnvelope(9, 0, TypeMerge, merge)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}
