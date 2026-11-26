//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// recordsMerge is a merge shaped as a fresh engine's placement merges are:
// 2,000 records, each with its object's JSON and its call, over the record
// before it.
func recordsMerge() Merge {
	object := json.RawMessage(`{"rootfs":{"id":"` + strings.Repeat("a", 64) + `"},"config":{"env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","GOTOOLCHAIN=auto"],"workdir":"/src","entrypoint":["/bin/sh","-c"]},"platform":"linux/arm64","mounts":[]}`)
	bundle := dagql.ValueBundle{Version: 9, Roots: []dagql.TransferredRoot{{Ordinal: 2000}}}
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

// A set file's part is encoded once, in place: what encoding a part of a
// file allocates is about its compressed bytes and its records' own values,
// not copies of the part's JSON. The JSON of a part of 2,000 records was
// written into a buffer that grew as it filled, then copied whole, before
// it was compressed; it is now written into the file's buffer, which an
// earlier part grew.
func TestSetFilePartIsEncodedInPlace(t *testing.T) {
	// One encoder, which the first part readies: an encoder's state is made
	// on its first use, once for the life of what encodes files.
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(1<<20), zstd.WithLowerEncoderMem(true), zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	defer enc.Close()
	bundle := recordsMerge().Bundle
	shared, err := dagql.ShareChainLayers(bundle)
	require.NoError(t, err)
	raw, err := json.Marshal(shared)
	require.NoError(t, err)
	var buf bytes.Buffer
	part, _, err := EncodeSetFilePart(enc, &buf, bundle)
	require.NoError(t, err)
	require.Equal(t, string(raw), buf.String(), "the part's JSON is json.Marshal's")
	allocated := allocatedBy(5, func() {
		if _, _, err := EncodeSetFilePart(enc, &buf, bundle); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.0f bytes allocated for a part of %d bytes of JSON, %d compressed: %.2f times its JSON", allocated, len(raw), len(part), allocated/float64(len(raw)))
	// 8.4 times its JSON when the JSON was copied as it grew and once whole;
	// 1.6 times written in place, most of it each record's own values.
	require.Less(t, allocated, 3*float64(len(raw)))
}
