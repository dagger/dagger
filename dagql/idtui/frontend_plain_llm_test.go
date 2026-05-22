package idtui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestPlainRunRendersConciseCheckLines(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	fe := NewPlain(&buf).(*frontendPlainLLM)
	rootID := prettyTestSpanID(1)
	passID := prettyTestSpanID(2)
	failID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "checks",
			StartTime: start,
			EndTime:   start.Add(3 * time.Second),
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "checks failed",
			},
			Final: true,
		},
		{
			ID:          passID,
			TraceID:     prettyTestTraceID(),
			Name:        "unit",
			CheckName:   "unit",
			CheckPassed: true,
			StartTime:   start.Add(time.Second),
			EndTime:     start.Add(2 * time.Second),
			ParentID:    rootID,
			Status: sdktrace.Status{
				Code: codes.Ok,
			},
			Final: true,
		},
		{
			ID:          failID,
			TraceID:     prettyTestTraceID(),
			Name:        "lint",
			CheckName:   "lint",
			CheckPassed: false,
			StartTime:   start.Add(2 * time.Second),
			EndTime:     start.Add(3 * time.Second),
			ParentID:    rootID,
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "lint failed\nrun gofmt",
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
	for _, want := range []string{
		"[+0.0s] started",
		`check PASS name="unit"`,
		`check FAIL name="lint" duration=1.0s error="lint failed run gofmt"`,
		"result=FAIL",
		"checks_failed=1",
		"lint failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain output missing %q:\n%s", want, got)
		}
	}
}

func TestPlainStatusLineIncludesActiveWorkAndResources(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf strings.Builder
	fe := NewPlain(&buf).(*frontendPlainLLM)
	rootID := prettyTestSpanID(1)
	runID := prettyTestSpanID(2)
	childID := prettyTestSpanID(3)
	start := time.Now().Add(-15 * time.Second)
	fe.started = start
	fe.lastStatus = start
	fe.opts = dagui.FrontendOpts{Verbosity: dagui.ShowCompletedVerbosity}
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   time.Time{},
			Final:     true,
		},
		{
			ID:        runID,
			TraceID:   prettyTestTraceID(),
			Name:      "upload source",
			StartTime: start,
			EndTime:   time.Time{},
			ParentID:  rootID,
			Final:     true,
		},
		{
			ID:        childID,
			TraceID:   prettyTestTraceID(),
			Name:      "Container.withExec",
			StartTime: start,
			EndTime:   time.Time{},
			ParentID:  runID,
			Final:     true,
		},
	})
	fe.db.SetPrimarySpan(rootID)
	fe.db.MetricsBySpan = map[dagui.SpanID]map[string][]metricdata.DataPoint[int64]{
		runID: {
			telemetry.FilesyncWrittenBytes: {{Value: 1024 * 1024}},
		},
	}

	fe.renderStatusLocked(true)

	got := stripANSITest(buf.String())
	for _, want := range []string{
		"progress",
		"running=2",
		`active="upload source`,
		`filesync_written="1.0 MB"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain status output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Container.withExec") {
		t.Fatalf("plain status should use the higher-level active row:\n%s", got)
	}
}
