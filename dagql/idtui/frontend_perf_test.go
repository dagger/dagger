package idtui

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/tuist"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func benchSpanID(n int) dagui.SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(n)+1)
	return dagui.SpanID{SpanID: id}
}

// benchSession models a long agent-style session: thousands of completed,
// collapsed tool calls and some checks with tests, each of which leaves a
// SpanTreeView behind, plus one running span streaming output.
type benchSession struct {
	fe      *frontendPretty
	rootID  dagui.SpanID
	liveID  dagui.SpanID
	checkID dagui.SpanID // the first check, a small subtree to zoom into
	logs    []sdklog.Record
}

func newBenchSession(b *testing.B, tools, checks, testsPerCheck int) *benchSession {
	b.Helper()
	db := dagui.NewDB()
	traceID := prettyTestTraceID()
	start := time.Unix(100, 0)
	end := start.Add(time.Second)

	next := 0
	newID := func() dagui.SpanID {
		id := benchSpanID(next)
		next++
		return id
	}
	rootID := newID()
	liveID := newID()
	snaps := []dagui.SpanSnapshot{
		{ID: rootID, TraceID: traceID, Name: "root", StartTime: start},
	}
	for i := range tools {
		toolID := newID()
		snaps = append(snaps, dagui.SpanSnapshot{
			ID: toolID, TraceID: traceID, ParentID: rootID,
			Name: fmt.Sprintf("tool %d", i), LLMRole: "assistant", LLMTool: "Run",
			StartTime: start, EndTime: end, Final: true,
		})
		for j := range 3 {
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: newID(), TraceID: traceID, ParentID: toolID,
				Name:      fmt.Sprintf("tool %d step %d", i, j),
				StartTime: start, EndTime: end, Final: true,
			})
		}
	}
	var checkID dagui.SpanID
	for i := range checks {
		id := newID()
		if i == 0 {
			checkID = id
		}
		snaps = append(snaps, dagui.SpanSnapshot{
			ID: id, TraceID: traceID, ParentID: rootID,
			Name: fmt.Sprintf("check %d", i), CheckName: fmt.Sprintf("check:%d", i),
			StartTime: start, EndTime: end, Final: true,
		})
		for j := range testsPerCheck {
			status := dagui.TestStatusSuccess
			if j == 0 {
				status = dagui.TestStatusFailure
			}
			testID := newID()
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: testID, TraceID: traceID, ParentID: id,
				Name: fmt.Sprintf("Test%d_%d", i, j), TestCaseName: fmt.Sprintf("Test%d_%d", i, j),
				TestStatus: status, StartTime: start, EndTime: end, Final: true,
			})
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: newID(), TraceID: traceID, ParentID: testID,
				Name: "exec go test", StartTime: start, EndTime: end, Final: true,
			})
		}
	}
	snaps = append(snaps, dagui.SpanSnapshot{
		ID: liveID, TraceID: traceID, ParentID: rootID, Name: "live exec", StartTime: start,
	})
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(rootID)

	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.SetPrimary(rootID)
	fe.tui.Step()

	logs := make([]sdklog.Record, 5)
	for i := range logs {
		logs[i] = frontendTestLogRecord(liveID.SpanID, otellog.StringValue(fmt.Sprintf("output line %d\n", i)))
	}
	return &benchSession{
		fe:      fe,
		rootID:  rootID,
		liveID:  liveID,
		checkID: checkID,
		logs:    logs,
	}
}

// BenchmarkLiveUpdates measures the UI goroutine's cost of streaming telemetry
// into a long session: per log or span batch handled synchronously
// ("dispatch"), and per frame rendered after ten queued batches ("frame").
// "zoomed" first renders the whole session, then zooms into one check,
// leaving the rest of the session's SpanTreeViews unmounted.
func BenchmarkLiveUpdates(b *testing.B) {
	const batchesPerFrame = 10
	for _, scenario := range []string{"full", "zoomed"} {
		setup := func(b *testing.B) *benchSession {
			s := newBenchSession(b, 4000, 20, 10)
			if scenario == "zoomed" {
				s.fe.ZoomToSpan(s.checkID)
				s.fe.tui.Step()
			}
			return s
		}
		logBatch := func(s *benchSession) {
			_ = s.fe.LogExporter().Export(context.Background(), s.logs)
		}
		spanBatch := func(s *benchSession) {
			s.fe.ImportSnapshots([]dagui.SpanSnapshot{{
				ID: s.liveID, TraceID: prettyTestTraceID(), ParentID: s.rootID,
				Name: "live exec", StartTime: time.Unix(100, 0),
			}})
		}
		for _, kind := range []struct {
			name  string
			batch func(*benchSession)
		}{
			{"logs", logBatch},
			{"spans", spanBatch},
		} {
			b.Run(scenario+"/"+kind.name+"/dispatch", func(b *testing.B) {
				s := setup(b)
				// Run dispatched work synchronously, isolating the per-batch
				// cost from rendering.
				s.fe.reportOnly = true
				defer func() { s.fe.reportOnly = false }()
				for b.Loop() {
					kind.batch(s)
				}
				b.ReportMetric(float64(len(s.fe.spanTrees)), "trees")
			})
			b.Run(scenario+"/"+kind.name+"/frame", func(b *testing.B) {
				s := setup(b)
				for b.Loop() {
					for range batchesPerFrame {
						kind.batch(s)
					}
					s.fe.tui.Step()
				}
				b.ReportMetric(float64(len(s.fe.spanTrees)), "trees")
			})
		}
	}
}
