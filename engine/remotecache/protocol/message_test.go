package protocol

import (
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A message of at most CompressAbove bytes of JSON goes as it is: the JSON
// EncodeEnvelope gives.
func TestSmallMessageGoesAsItIs(t *testing.T) {
	t.Parallel()
	body := Settled{}
	want, err := EncodeEnvelope(3, 7, TypeSettled, body)
	require.NoError(t, err)
	require.LessOrEqual(t, len(want), CompressAbove)
	msg, err := EncodeMessage(3, 7, TypeSettled, body, MaxMessageBytes)
	require.NoError(t, err)
	require.False(t, msg.Compressed)
	require.Equal(t, string(want), string(msg.Data))
	require.Equal(t, len(want), msg.Size)
	got, err := DecodeMessage(msg.Data, false, MaxMessageBytes)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// A larger message goes compressed, and decodes to the JSON EncodeEnvelope
// gives, which it declares the length of.
func TestLargeMessageGoesCompressed(t *testing.T) {
	t.Parallel()
	merge := recordsMerge()
	want, err := EncodeEnvelope(9, 0, TypeMerge, merge)
	require.NoError(t, err)
	msg, err := EncodeMessage(9, 0, TypeMerge, merge, MaxMessageBytes)
	require.NoError(t, err)
	require.True(t, msg.Compressed)
	require.Equal(t, len(want), msg.Size)
	require.Less(t, len(msg.Data), len(want)/4, "compressed, the %d-byte message", len(want))
	declared, k := binary.Uvarint(msg.Data)
	require.Positive(t, k)
	require.Equal(t, uint64(len(want)), declared)
	got, err := DecodeMessage(msg.Data, true, MaxMessageBytes)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// A message over the limit stops encoding there and is refused; one exactly
// at it goes.
func TestMessageOverTheLimitIsRefused(t *testing.T) {
	t.Parallel()
	merge := recordsMerge()
	want, err := EncodeEnvelope(9, 0, TypeMerge, merge)
	require.NoError(t, err)
	_, err = EncodeMessage(9, 0, TypeMerge, merge, len(want)-1)
	require.ErrorIs(t, err, ErrTooLarge)
	msg, err := EncodeMessage(9, 0, TypeMerge, merge, len(want))
	require.NoError(t, err)
	require.Equal(t, len(want), msg.Size)
}

// A reader checks the length a compressed message declares, and doesn't
// trust it: one over the limit is refused before anything is decompressed,
// and a frame that decompresses to another length than it declares is
// refused too.
func TestDecodeMessageChecksTheDeclaredLength(t *testing.T) {
	t.Parallel()
	msg, err := EncodeMessage(9, 0, TypeMerge, recordsMerge(), MaxMessageBytes)
	require.NoError(t, err)
	_, k := binary.Uvarint(msg.Data)
	frame := msg.Data[k:]
	declaring := func(size uint64) []byte {
		data := binary.AppendUvarint(nil, size)
		return append(data, frame...)
	}

	_, err = DecodeMessage(declaring(uint64(msg.Size)), true, msg.Size-1)
	require.ErrorIs(t, err, ErrTooLarge, "declared over the limit")
	_, err = DecodeMessage(append(binary.AppendUvarint(nil, uint64(msg.Size)), "not a frame"...), true, msg.Size-1)
	require.ErrorIs(t, err, ErrTooLarge, "refused before it is decompressed")
	_, err = DecodeMessage(declaring(uint64(msg.Size)+1), true, MaxMessageBytes)
	require.ErrorContains(t, err, "declares", "decompressed to fewer bytes than declared")
	_, err = DecodeMessage(declaring(uint64(msg.Size)-1), true, MaxMessageBytes)
	require.Error(t, err, "decompressed to more bytes than declared")
	_, err = DecodeMessage(nil, true, MaxMessageBytes)
	require.Error(t, err, "no length")
}

// A message cut short never decodes: a compressed one fails to decompress
// wherever it is cut, and a plain one, cut, isn't JSON.
func TestCutMessageNeverDecodes(t *testing.T) {
	t.Parallel()
	msg, err := EncodeMessage(9, 0, TypeMerge, recordsMerge(), MaxMessageBytes)
	require.NoError(t, err)
	for cut := 1; cut < len(msg.Data); cut += max(1, len(msg.Data)/64) {
		_, err := DecodeMessage(msg.Data[:cut], true, MaxMessageBytes)
		require.Error(t, err, "cut at %d of %d bytes", cut, len(msg.Data))
	}
	small, err := EncodeMessage(3, 0, TypeProbe, Probe{}, MaxMessageBytes)
	require.NoError(t, err)
	require.False(t, small.Compressed)
	for cut := 1; cut < len(small.Data); cut++ {
		data, err := DecodeMessage(small.Data[:cut], false, MaxMessageBytes)
		require.NoError(t, err)
		var env Envelope
		require.Error(t, json.Unmarshal(data, &env), "cut at %d of %d bytes", cut, len(small.Data))
	}
}

// At most as many encoders are kept idle as the pool keeps; a message takes
// one of those first, and makes one only when none is left.
func TestIdleEncodersAreBounded(t *testing.T) {
	t.Parallel()
	encoders := newIdleEncoders(2)
	a, err := encoders.get()
	require.NoError(t, err)
	b, err := encoders.get()
	require.NoError(t, err)
	c, err := encoders.get()
	require.NoError(t, err)
	encoders.put(a)
	encoders.put(b)
	encoders.put(c)
	require.Len(t, encoders.idle, 2, "the third is let go")
	got, err := encoders.get()
	require.NoError(t, err)
	require.Same(t, a, got)
	got, err = encoders.get()
	require.NoError(t, err)
	require.Same(t, b, got)
	got, err = encoders.get()
	require.NoError(t, err)
	require.NotSame(t, c, got, "none kept: a new one")
}
