package cloudpull

import (
	"encoding/json"
	"testing"
	"time"

	cloud "github.com/dagger/dagger/internal/cloud"
)

func TestSpansToRecords(t *testing.T) {
	t.Parallel()

	parentID := "aaaaaaaaaaaaaaaa"
	now := time.Unix(100, 0)
	end := now.Add(2 * time.Second)
	updated := now.Add(3 * time.Second)

	recs, err := spansToRecords([]cloud.SpanData{
		{
			ID:         "bbbbbbbbbbbbbbbb",
			TraceID:    "cccccccccccccccccccccccccccccccc",
			ParentID:   &parentID,
			Name:       "Query.container",
			Timestamp:  now,
			EndTime:    &end,
			UpdateTime: updated,
			Attributes: map[string]any{
				"dagger.io/dag.digest": "call1",
			},
			Status: cloud.SpanStatus{
				Code:    "STATUS_CODE_OK",
				Message: "",
			},
			Events: []cloud.SpanEvent{
				{
					Timestamp: now.Add(time.Second),
					Name:      "event1",
					Attributes: map[string]any{
						"k": "v",
					},
				},
			},
			Scope: cloud.SpanScope{
				Name:    "dagger.io/dagql",
				Version: "1.0.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("spansToRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	rec := recs[0]
	if rec.TraceID != "cccccccccccccccccccccccccccccccc" {
		t.Fatalf("unexpected trace id: %q", rec.TraceID)
	}
	if rec.ParentSpanID != parentID {
		t.Fatalf("unexpected parent span id: %q", rec.ParentSpanID)
	}
	if rec.StatusCode != "STATUS_CODE_OK" {
		t.Fatalf("unexpected status code: %q", rec.StatusCode)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(rec.DataJSON), &payload); err != nil {
		t.Fatalf("unmarshal data_json: %v", err)
	}
	if attrs, ok := payload["attributes"].(map[string]any); !ok || attrs["dagger.io/dag.digest"] != "call1" {
		t.Fatalf("unexpected attrs payload: %#v", payload["attributes"])
	}
}
