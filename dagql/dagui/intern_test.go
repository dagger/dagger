package dagui

import (
	"context"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/spanfootprint"
)

func TestDigestTable(t *testing.T) {
	var table digestTable
	for _, dig := range []string{
		"xxh3:0123456789abcdef", "xxh3:0123456789ABCDEF", "xxh3:0123456789abcde",
		"sha256:0123", "0123456789abcdef", "xxh3:ffffffffffffffff", "",
	} {
		// A fresh copy each time: the table must hand back the first one.
		first := table.intern(string([]byte(dig)))
		require.Equal(t, dig, first)
		again := table.intern(string([]byte(dig)))
		require.Equal(t, dig, again)
		if dig != "" {
			require.Same(t, unsafe.StringData(first), unsafe.StringData(again), dig)
		}
	}
}

// TestIngestSharesDigests checks that a DB fed the wire trace keeps one
// copy of each call digest -- the span's dag.digest and dag.output, its
// frame's digest, and the frames and spans naming it -- and that sharing
// changes no value.
func TestIngestSharesDigests(t *testing.T) {
	w := wireSpans{spanfootprint.New(3_000)}
	db := NewDB()
	require.NoError(t, w.ExportSpans(context.Background(), db))
	data := func(s string) *byte { return unsafe.StringData(s) }
	var calls, shared int
	for _, span := range db.Spans.Order {
		if span.CallDigest == "" || span.Output == "" {
			continue
		}
		calls++
		frame := db.Calls[span.CallDigest]
		require.NotNil(t, frame, span.CallDigest)
		require.Equal(t, span.CallDigest, frame.Digest)
		require.Equal(t, span.CallDigest, span.Output, "the wire trace's outputs are the calls' own recipes")
		require.Same(t, data(span.CallDigest), data(frame.Digest))
		require.Same(t, data(span.CallDigest), data(span.Output))
		require.Len(t, span.Inputs, 2)
		require.Equal(t, frame.ReceiverDigest, span.Inputs[0])
		require.Same(t, data(frame.ReceiverDigest), data(span.Inputs[0]))
		if receiver := db.Calls[frame.ReceiverDigest]; receiver != nil {
			require.Same(t, data(receiver.Digest), data(frame.ReceiverDigest))
			shared++
		}
		require.Same(t, data(frame.Field), data(db.internName(frame.Field)))
	}
	require.NotZero(t, calls)
	require.NotZero(t, shared)
}
