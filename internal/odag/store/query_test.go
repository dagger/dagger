package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTraceQueries(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "odag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	traceID := "feedfeedfeedfeedfeedfeedfeedfeed"
	_, err = st.UpsertSpans(ctx, "collector", []SpanRecord{
		{
			TraceID:         traceID,
			SpanID:          "1111111111111111",
			Name:            "Query.demo",
			StartUnixNano:   100,
			EndUnixNano:     200,
			StatusCode:      "STATUS_CODE_OK",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"d1"}}`,
			UpdatedUnixNano: 201,
		},
	})
	if err != nil {
		t.Fatalf("upsert spans: %v", err)
	}

	traces, err := st.ListTraces(ctx, 10)
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].TraceID != traceID {
		t.Fatalf("unexpected trace id: %s", traces[0].TraceID)
	}

	trace, err := st.GetTrace(ctx, traceID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if trace.TraceID != traceID || trace.SpanCount != 1 {
		t.Fatalf("unexpected trace record: %#v", trace)
	}

	spans, err := st.ListTraceSpans(ctx, traceID)
	if err != nil {
		t.Fatalf("list trace spans: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].SpanID != "1111111111111111" {
		t.Fatalf("unexpected span record: %#v", spans[0])
	}
}
