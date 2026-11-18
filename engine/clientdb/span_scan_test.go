package clientdb

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestScanSpansLatest checks that a scan summarizes exactly the rows
// SelectSpansLatest returns, from spilled frames and the tail alike.
func TestScanSpansLatest(t *testing.T) {
	ctx := t.Context()
	// A tiny tail budget spills most rows to the file.
	store, err := openStore(ctx, t.TempDir(), "scan", 512)
	require.NoError(t, err)
	defer store.Close()
	var rows []Span
	for i := range 200 {
		row := Span{
			TraceID: "000102030405060708090a0b0c0d0e0f", SpanID: fmt.Sprintf("%016x", i%150+1),
			Name: fmt.Sprintf("span %d", i), StartTime: int64(i), StatusCode: int64(i % 3),
			Attributes: []byte(fmt.Sprintf("attrs %d", i)), Links: []byte("links"),
		}
		if i%2 == 0 {
			row.EndTime = sql.NullInt64{Int64: int64(i + 1), Valid: true}
		}
		if i > 0 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprintf("%016x", i/2+1), Valid: true}
		}
		rows = append(rows, row)
		// Small batches, so spills interleave with appends.
		if len(rows) == 7 || i == 199 {
			_, err := store.AppendSpans(rows)
			require.NoError(t, err)
			rows = nil
		}
		if i == 120 {
			// Rows so far are spilled for sure; the rest may stay in the tail.
			require.NoError(t, store.spans.flush(ctx))
		}
	}
	ids := store.SpanIDs()
	want, err := store.SelectSpansLatest(ctx, ids)
	require.NoError(t, err)
	require.Len(t, want, 150)
	var got []Span
	require.NoError(t, store.ScanSpansLatest(ctx, ids, func(s *SpanSummary) error {
		got = append(got, Span{
			ID: s.ID, TraceID: string(s.TraceID), SpanID: string(s.SpanID),
			ParentSpanID: sql.NullString{String: string(s.ParentSpanID), Valid: s.HasParent},
			Name:         string(s.Name), StartTime: s.StartTime,
			EndTime:    sql.NullInt64{Int64: s.EndTime, Valid: s.Ended},
			StatusCode: s.StatusCode, Attributes: append([]byte(nil), s.Attributes...),
		})
		return nil
	}))
	require.Len(t, got, len(want))
	for i := range want {
		w := want[i]
		w.Links = nil
		require.Equal(t, w, got[i])
	}
}
