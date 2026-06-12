package idtui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/util/cleanups"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestDotsRunRendersFinalFailureReport(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	fe := NewDots(&buf).(*frontendDots)
	rootID := prettyTestSpanID(1)
	failedID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "failing root",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "command failed",
			},
			Final: true,
		},
		{
			ID:        failedID,
			TraceID:   prettyTestTraceID(),
			Name:      "failed step",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "dagger exploded",
			},
			Final: true,
		},
	})
	fe.SetPrimary(rootID)

	err := fe.Run(context.Background(), dagui.FrontendOpts{}, func(context.Context) (cleanups.CleanupF, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected failed primary span to produce an exit error")
	}

	got := stripANSITest(buf.String())
	if !strings.Contains(got, "failed step") || !strings.Contains(got, "dagger exploded") {
		t.Fatalf("dots final output did not include failure report:\n%s", got)
	}
}
