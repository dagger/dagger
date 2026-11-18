package clientdb

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// TestLogLookupMatchesReference feeds random log rows -- canonical and
// malformed span IDs and digests, rows without a valid span ID, display and
// semantic records, malformed attributes, spans whose snapshot arrives
// before or after their rows -- to the interned log index and to the
// string-keyed one it replaced (refLogLookup), and requires every answer to
// be the same, after every batch.
func TestLogLookupMatchesReference(t *testing.T) {
	for seed := range uint64(30) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed*13+5))
			spans := []string{"", "x", "SPAN", "0123456789ABCDEF"}
			for i := range 20 {
				spans = append(spans, fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15))
			}
			digests := []string{"", "", "sha256:abc", "xxh3:0123", "xxh3:0123456789ABCDEF"}
			for i := range 6 {
				digests = append(digests, fmt.Sprintf("xxh3:%016x", uint64(i+7)*0x9e3779b97f4a7c15))
			}
			traces := []string{selectionTrace, "02000000000000000000000000000000", ""}
			pick := func(from []string) string { return from[rng.IntN(len(from))] }
			row := func(id int64) Log {
				var attrs []*commonpb.KeyValue
				if d := pick(digests); d != "" {
					attrs = append(attrs, selectionAttr(telemetry.DagDigestAttr, d))
				}
				switch rng.IntN(5) {
				case 0:
					attrs = append(attrs, selectionAttr(telemetryattrs.ProgressItemAttr, "item"))
				case 1:
					attrs = append(attrs, selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
				}
				body := selectionText(fmt.Sprintf("line %d", id))
				if rng.IntN(6) == 0 {
					body = selectionText("")
				}
				r := selectionLog(t, pick(spans), body, attrs...)
				r.ID, r.Timestamp = id, int64(rng.IntN(1000))
				r.TraceID.String = pick(traces)
				r.TraceID.Valid = r.TraceID.String != ""
				switch rng.IntN(8) {
				case 0:
					r.SpanID.Valid = false // the codec keeps String anyway
				case 1:
					r.Attributes = []byte{0xff, 0x01}
				}
				return r
			}

			known := map[string]bool{}
			spanKnown := func(id string) bool { return known[id] }
			got, want := newLogLookup(), newRefLogLookup()
			got.spanKnown, want.spanKnown = spanKnown, spanKnown
			var id int64
			for batch := range 15 {
				var rows []Log
				for range 1 + rng.IntN(10) {
					id++
					rows = append(rows, row(id))
				}
				got.addAll(rows)
				want.addAll(rows)
				// Some spans' snapshots arrive.
				var stored []Span
				for range rng.IntN(3) {
					s := pick(spans)
					known[s] = true
					stored = append(stored, Span{SpanID: s})
				}
				got.spansStored(stored)
				want.spansStored(stored)

				msg := fmt.Sprintf("batch %d", batch)
				require.Len(t, got.meta, len(want.meta), msg)
				for i, w := range want.meta {
					g := got.meta[i]
					require.Equal(t, w.timestamp, g.timestamp, msg)
					require.Equal(t, w.trace, g.trace, msg)
					require.Equal(t, w.span, g.span, msg)
					require.Equal(t, w.digest, g.digest, msg)
					require.Equal(t, w.flags, g.flags(), msg)
					require.Equal(t, min(w.size, logMetaMaxSize), g.size(), msg)
				}
				require.Equal(t, want.metaErrs, got.metaErrs, msg)
				require.Equal(t, want.traceIDs, got.traceIDs, msg)
				require.Equal(t, len(want.spanIDs), got.spanCount(), msg)
				for n, name := range want.spanIDs {
					require.Equal(t, name, got.spanName(uint32(n)), msg)
				}
				require.Equal(t, len(want.digests), got.digests.count(), msg)
				for n, name := range want.digests {
					require.Equal(t, name, got.digestName(uint32(n)), msg)
				}
				require.Equal(t, want.displayByDigest, got.displayByDigest, msg)
				require.Equal(t, want.unspanned, got.unspanned, msg)
				require.Equal(t, want.unplaced, got.unplaced, msg)
				require.ElementsMatch(t, want.unplacedSpanIDs(), got.unplacedSpanIDs(), msg)
				for _, s := range append(spans, "unknown") {
					require.Equal(t, want.spanRowsLocked(s, nil), got.spanRowsLocked(s, nil), "%s span %q", msg, s)
					for _, tail := range []int{0, 1, 3} {
						set := map[string]struct{}{s: {}, pick(spans): {}}
						require.Equal(t, want.rowIDsForSpans(set, tail), got.rowIDsForSpans(set, tail), "%s span %q", msg, s)
					}
				}
			}
		})
	}
}
