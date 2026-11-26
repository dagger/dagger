//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

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

// writeRecorder records what is written to it: every byte, and the largest
// single write.
type writeRecorder struct {
	buf     bytes.Buffer
	largest int
}

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.largest = max(w.largest, len(p))
	return w.buf.Write(p)
}

// byteCount counts the bytes written to it and keeps none.
type byteCount int

func (c *byteCount) Write(p []byte) (int, error) {
	*c += byteCount(len(p))
	return len(p), nil
}

// WriteEnvelope writes the message EncodeEnvelope returns as it is encoded:
// the same bytes, handed over in pieces far smaller than the message, and,
// to a writer that keeps nothing, for a fraction of what encoding it whole
// allocates.
func TestWriteEnvelopeWritesTheMessageAsItIsEncoded(t *testing.T) {
	merge := recordsMerge()
	want, err := EncodeEnvelope(9, 0, TypeMerge, merge)
	require.NoError(t, err)
	var w writeRecorder
	require.NoError(t, WriteEnvelope(&w, 9, 0, TypeMerge, merge))
	require.Equal(t, string(want), w.buf.String())
	require.Less(t, w.largest, len(want)/8, "the largest write of a %d-byte message", len(want))
	whole := allocatedBy(5, func() {
		if _, err := EncodeEnvelope(9, 0, TypeMerge, merge); err != nil {
			t.Fatal(err)
		}
	})
	written := allocatedBy(5, func() {
		var n byteCount
		if err := WriteEnvelope(&n, 9, 0, TypeMerge, merge); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("a %d-byte message: %.0f bytes allocated encoded whole, %.0f written as encoded", len(want), whole, written)
	require.Less(t, written, float64(len(want)), "written as encoded, it allocates less than the message itself")
}

// errFull is what a fullWriter refuses with.
var errFull = errors.New("full")

// fullWriter takes room bytes, then refuses every write.
type fullWriter struct {
	room, written int
	refused       int
}

func (w *fullWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.room {
		w.refused++
		return 0, errFull
	}
	w.written += len(p)
	return len(p), nil
}

// A writer that refuses more bytes stops the encoding: WriteEnvelope
// returns its error, and writes nothing after it.
func TestWriteEnvelopeStopsAtAWriterThatRefuses(t *testing.T) {
	t.Parallel()
	merge := recordsMerge()
	w := &fullWriter{room: 64 << 10}
	err := WriteEnvelope(w, 9, 0, TypeMerge, merge)
	require.ErrorIs(t, err, errFull)
	require.Equal(t, 1, w.refused, "no write after the first refused")
	require.LessOrEqual(t, w.written, w.room)
}
