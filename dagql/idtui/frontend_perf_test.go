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
	toolID  dagui.SpanID // the first tool call
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
	var firstToolID dagui.SpanID
	for i := range tools {
		toolID := newID()
		if i == 0 {
			firstToolID = toolID
		}
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
		toolID:  firstToolID,
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

// addToolTestSuite makes the session's first tool call (a Boundary, like a
// real one) run a nested test suite of the given number of cases: an exec, a
// suite, parent tests of 100 cases each, and several levels of plain spans
// between each parent and its cases, about ten hops from a case to the tool.
func (s *benchSession) addToolTestSuite(cases int) {
	traceID := prettyTestTraceID()
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	next := 1 << 24 // clear of newBenchSession's IDs
	newID := func() dagui.SpanID {
		id := benchSpanID(next)
		next++
		return id
	}
	span := func(id, parent dagui.SpanID, name string) dagui.SpanSnapshot {
		return dagui.SpanSnapshot{
			ID: id, TraceID: traceID, ParentID: parent, Name: name,
			StartTime: start, EndTime: end, Final: true,
		}
	}
	tool := span(s.toolID, s.rootID, "tool 0")
	tool.LLMRole, tool.LLMTool, tool.Boundary = "assistant", "Run", true
	exec := span(newID(), s.toolID, "exec go test")
	suite := span(newID(), exec.ID, "pkg")
	suite.TestSuiteName = "pkg"
	snaps := []dagui.SpanSnapshot{tool, exec, suite}
	const casesPerGroup, wrappers = 100, 6
	for g := 0; g*casesPerGroup < cases; g++ {
		group := span(newID(), suite.ID, fmt.Sprintf("TestGroup%d", g))
		group.TestCaseName = group.Name
		group.TestSuiteName = "pkg"
		group.TestStatus = dagui.TestStatusSuccess
		snaps = append(snaps, group)
		parent := group.ID
		for w := range wrappers {
			wrapper := span(newID(), parent, fmt.Sprintf("run %d", w))
			snaps = append(snaps, wrapper)
			parent = wrapper.ID
		}
		for c := g * casesPerGroup; c < min(cases, (g+1)*casesPerGroup); c++ {
			tc := span(newID(), parent, fmt.Sprintf("TestGroup%d/case_%d", g, c))
			tc.TestCaseName = tc.Name
			tc.TestSuiteName = "pkg"
			tc.TestStatus = dagui.TestStatusSuccess
			snaps = append(snaps, tc)
		}
	}
	s.fe.ImportSnapshots(snaps)
	s.fe.tui.Step()
}

// BenchmarkToolTestSuite measures frames of a long agent session in which one
// tool call ran a large test suite, with unrelated span updates streaming in
// between frames. Every check and collapsed tool row asks for its own scoped
// test view each frame, so a frame must cost about the same with the suite as
// without it -- not (rows × tests in the session × ancestor depth).
func BenchmarkToolTestSuite(b *testing.B) {
	for _, cases := range []int{0, 10000} {
		b.Run(fmt.Sprintf("cases=%d", cases), func(b *testing.B) {
			s := newBenchSession(b, 1000, 0, 0)
			s.addToolTestSuite(cases)
			for b.Loop() {
				s.fe.ImportSnapshots([]dagui.SpanSnapshot{{
					ID: s.liveID, TraceID: prettyTestTraceID(), ParentID: s.rootID,
					Name: "live exec", StartTime: time.Unix(100, 0),
				}})
				s.fe.tui.Step()
			}
		})
	}
}
