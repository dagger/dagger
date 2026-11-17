package clientdb

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/engine/archive"
)

// Spike for priority-first live loading (not a feature test): can the
// archive span view (SpanViewAt) answer the live CLI's selection over a store
// that is still being appended to, and what does following it cost?

func liveViewSpan(t testing.TB, id, parent string, attrs ...*commonpb.KeyValue) Span {
	t.Helper()
	row := Span{TraceID: selectionTrace, SpanID: id}
	if parent != "" {
		row.ParentSpanID = sql.NullString{String: parent, Valid: true}
	}
	if len(attrs) > 0 {
		encoded, err := MarshalProtoList(attrs)
		require.NoError(t, err)
		row.Attributes = encoded
	}
	return row
}

func liveViewSelected(v *SpanView) []string {
	var ids []string
	for id := range v.nodes {
		if v.selected == nil || v.selected[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestSpikeSpanViewFollowsLiveStore: a view at each successive HighWater of a
// growing store is correct for that cut -- a priority span arriving late
// pulls in ancestors whose rows predate the previous cut, and an already
// selected span's child count grows -- so a follower has to (a) re-send old
// rows that become selected and (b) re-send selected spans whose annotations
// changed: neither is "rows after the cursor".
func TestSpikeSpanViewFollowsLiveStore(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()

	// root -> a -> b -> c, plus root -> x (all plain)
	_, err = db.AppendSpans([]Span{
		liveViewSpan(t, "root", ""),
		liveViewSpan(t, "a", "root"),
		liveViewSpan(t, "b", "a"),
		liveViewSpan(t, "c", "b"),
		liveViewSpan(t, "x", "root"),
	})
	require.NoError(t, err)
	cut1 := db.HighWater()
	v1, err := db.SpanViewAt(t.Context(), selectionTrace, cut1, &archive.SpanSelection{DagUIView: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"root", "a", "x"}, liveViewSelected(v1),
		"the root and its visible children")
	require.EqualValues(t, 1, v1.Attributes("a")[0].Value.GetIntValue(), "a's child count at cut1")

	// Later: a test case deep beneath c, and another child of a.
	_, err = db.AppendSpans([]Span{
		liveViewSpan(t, "test", "c", &commonpb.KeyValue{
			Key:   string(semconv.TestCaseNameKey),
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "TestX"}},
		}),
		liveViewSpan(t, "a2", "a"),
	})
	require.NoError(t, err)
	cut2 := db.HighWater()
	require.Greater(t, cut2.Spans, cut1.Spans)
	v2, err := db.SpanViewAt(t.Context(), selectionTrace, cut2, &archive.SpanSelection{DagUIView: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"root", "a", "b", "c", "test", "x"}, liveViewSelected(v2),
		"the late test pulls in b and c, whose rows are before cut1")
	for _, id := range []string{"b", "c"} {
		require.LessOrEqual(t, v2.nodes[id].row, cut1.Spans, "%s's row predates the previous cut", id)
	}
	require.EqualValues(t, 2, v2.Attributes("a")[0].Value.GetIntValue(),
		"a's child count grew, but a has no new row")
	require.False(t, v2.selected["a2"], "a's new child stays unloaded until a is listened")

	// Listening to a selects its new child as it arrives too.
	v3, err := db.SpanViewAt(t.Context(), selectionTrace, cut2, &archive.SpanSelection{NoRoot: true, Listen: []string{"a"}, DagUIView: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"root", "a", "b", "a2"}, liveViewSelected(v3))
}

// BenchmarkSpikeSpanViewPerCut measures what following a live store with
// SpanViewAt costs: every new cut misses the topology cache and rebuilds it
// from every span row, so a follower polling it pays O(spans) per poll, and
// O(spans^2) over a session.
func BenchmarkSpikeSpanViewPerCut(b *testing.B) {
	for _, n := range []int{10_000, 100_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			db, err := openStore(b.Context(), b.TempDir(), "client", telemetryTailBudget)
			require.NoError(b, err)
			defer db.Close()
			rows := make([]Span, 0, 1000)
			for i := range n {
				parent := ""
				if i > 0 {
					parent = fmt.Sprintf("%016x", max(0, i-1-i%48))
				}
				rows = append(rows, liveViewSpan(b, fmt.Sprintf("%016x", i), parent))
				if len(rows) == cap(rows) {
					_, err := db.AppendSpans(rows)
					require.NoError(b, err)
					rows = rows[:0]
				}
			}
			_, err = db.AppendSpans(rows)
			require.NoError(b, err)
			next := n
			for b.Loop() {
				// One new span, so the view is at a new cut, as a poll of a
				// live session is.
				_, err := db.AppendSpans([]Span{liveViewSpan(b, fmt.Sprintf("%016x", next), "0000000000000000")})
				require.NoError(b, err)
				next++
				if _, err := db.SpanViewAt(b.Context(), selectionTrace, db.HighWater(), &archive.SpanSelection{DagUIView: true}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
