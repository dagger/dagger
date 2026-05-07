package idtui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestPlainFinalRenderShowsHiddenRootErrorCauseLogs(t *testing.T) {
	var out strings.Builder
	fe := NewPlain(&out).(*frontendPlain)
	defer fe.ticker.Stop()
	fe.output = NewOutput(&out)

	traceID := trace.TraceID{1}
	causeID := dagui.SpanID{SpanID: trace.SpanID{2}}
	start := time.Unix(1, 0)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID:        causeID,
		TraceID:   dagui.TraceID{TraceID: traceID},
		Name:      "withExec uv lock",
		StartTime: start,
		EndTime:   start.Add(time.Second),
		Status: sdktrace.Status{
			Code:        codes.Error,
			Description: "exit code: 2",
		},
	}})
	fe.data[causeID] = &spanData{
		ready: true,
		logs: []logLine{{
			line: newCursorBuffer([]byte("Caused by: Failed to fetch: `https://pypi.example.com/simple`")),
			time: start.Add(time.Second),
		}},
	}

	fe.renderRootErrorCauses(fmt.Errorf("failed [traceparent:%s-%s]", traceID, causeID.SpanID))

	got := out.String()
	if !strings.Contains(got, "Failed to fetch") {
		t.Fatalf("expected root cause logs to render, got:\n%s", got)
	}
	if !fe.data[causeID].ended {
		t.Fatal("expected rendered root cause to be marked ended")
	}
}
