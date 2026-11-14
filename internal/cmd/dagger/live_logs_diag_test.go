package daggercmd

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/util/cleanups"
)

// diagLines makes l warn into the lines it returns, each its message and
// arguments joined by spaces.
func diagLines(l *liveLogs) func() []string {
	var mu sync.Mutex
	var lines []string
	l.diag = newLiveLogsDiag(func(msg string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, strings.TrimSpace(fmt.Sprintln(append([]any{msg}, args...)...)))
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(lines)
	}
}

// importRoutes makes l's reads route the spans in unknown nowhere
// (idtui.LogUnknown), and every other span to itself, requested.
func importRoutes(l *liveLogs, unknown map[uint16]bool) {
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogRoute {
		routes := make([]idtui.LogRoute, len(targets))
		for i, target := range targets {
			routes[i] = idtui.LogRoute{Coverage: idtui.LogCovered, Span: target.Span}
			if unknown[uint16(target.Span.SpanID[6])<<8|uint16(target.Span.SpanID[7])] {
				routes[i] = idtui.LogRoute{Coverage: idtui.LogUnknown}
			}
		}
		return routes
	}
}

// importer imports a batch of rows as read's: a read from the store's start,
// a catch-up, unless read is 0, the follow's.
func importer(t *testing.T, l *liveLogs) func(read uint64, rows ...liveLogRow) {
	return func(read uint64, rows ...liveLogRow) {
		t.Helper()
		imp := l.importFromStart
		if read == 0 {
			imp = l.importLogs
		}
		require.NoError(t, imp(t.Context(), liveLogRequest(rows), read, 0))
	}
}

// TestLiveLogsReplayWarning: a pair imported unclaimed, then claimed by a
// read from the start, is warned about once, with the roll-ups followed; one
// the follow claims first is not, nor what reads import of a pair they do
// not own.
func TestLiveLogsReplayWarning(t *testing.T) {
	l := newLiveLogs(nil, "trace", &bodyCollector{}, true)
	lines := diagLines(l)
	unknown := map[uint16]bool{8: true, 9: true}
	importRoutes(l, unknown)
	read := importer(t, l)
	l.spans[liveTestSpan(1).String()] = archive.LogSpan{SpanID: liveTestSpan(1).String(), Descendants: true}
	l.spans[liveTestSpan(2).String()] = archive.LogSpan{SpanID: liveTestSpan(2).String()}

	read(0, liveLogRow{span: 1, body: "p"}, liveLogRow{span: 8, body: "a"}, liveLogRow{span: 9, body: "b"}, liveLogRow{span: 9, body: "c"})
	require.Len(t, l.unclaimed, 2)
	clear(unknown)
	// The follow claims 9 first: what a catch-up reads of it again, it does
	// not own.
	read(0, liveLogRow{span: 9, body: "d"})
	read(3, liveLogRow{span: 9, body: "b"}, liveLogRow{span: 9, body: "c"})
	require.Empty(t, lines())
	// A catch-up claims 8, importing its rows again.
	read(4, liveLogRow{span: 8, body: "a"}, liveLogRow{span: 8, body: "e"})
	read(5, liveLogRow{span: 8, body: "a"})
	require.Equal(t, []string{
		"live logs: replay span " + liveTestSpan(8).String() + " digest  unclaimedRecords 1 unclaimedRead 0 claimedRecords 2 claimedRead 4 rollUps " + liveTestSpan(1).String() + " rollUpCount 1",
	}, lines())
	require.Empty(t, l.unclaimed)
	require.Empty(t, l.unclaimedOrder)
}

// TestLiveLogsUnclaimedReplay: a catch-up that imports a pair unclaimed
// again, its span still unplaced, is warned about once, and so is the next
// catch-up claiming it.
func TestLiveLogsUnclaimedReplay(t *testing.T) {
	l := newLiveLogs(nil, "trace", &bodyCollector{}, true)
	lines := diagLines(l)
	unknown := map[uint16]bool{8: true}
	importRoutes(l, unknown)
	read := importer(t, l)
	span := liveTestSpan(8).String()

	read(0, liveLogRow{span: 8, body: "a"})
	read(4, liveLogRow{span: 8, body: "a"})
	read(4, liveLogRow{span: 8, body: "b"})
	clear(unknown)
	read(5, liveLogRow{span: 8, body: "a"})
	require.Equal(t, []string{
		"live logs: unclaimed replay span " + span + " digest  unclaimedRecords 1 unclaimedRead 0 records 1 read 4 rollUps  rollUpCount 0",
		"live logs: replay span " + span + " digest  unclaimedRecords 3 unclaimedRead 4 claimedRecords 1 claimedRead 5 rollUps  rollUpCount 0",
	}, lines())
}

// TestLiveLogsNoReplayReadingOn: a read that goes on from where it, or the
// span, was -- the rest of the read that imported a pair unclaimed, a gap
// read from where its span retired -- imports nothing twice, unless the
// follow imported the pair since the span retired.
func TestLiveLogsNoReplayReadingOn(t *testing.T) {
	l := newLiveLogs(nil, "trace", &bodyCollector{}, true)
	lines := diagLines(l)
	unknown := map[uint16]bool{5: true, 7: true, 8: true, 9: true}
	importRoutes(l, unknown)
	read := importer(t, l)
	gap := func(rows ...liveLogRow) {
		t.Helper()
		require.NoError(t, l.importRead(t.Context(), liveLogRequest(rows), 6, 0, 2))
	}
	read(3, liveLogRow{span: 8, body: "a"})
	read(0, liveLogRow{span: 9, body: "b"})
	// The gap read's span retires here.
	require.Equal(t, uint64(2), l.imports)
	read(0, liveLogRow{span: 7, body: "c"})
	// A catch-up's rows are behind the gap read's cursor.
	read(4, liveLogRow{span: 5, body: "f"})
	clear(unknown)
	read(3, liveLogRow{span: 8, body: "d"})
	gap(liveLogRow{span: 9, body: "e"}, liveLogRow{span: 5, body: "g"})
	require.Empty(t, lines())
	gap(liveLogRow{span: 7, body: "c"})
	require.Equal(t, []string{
		"live logs: replay span " + liveTestSpan(7).String() + " digest  unclaimedRecords 1 unclaimedRead 0 claimedRecords 1 claimedRead 6 rollUps  rollUpCount 0",
	}, lines())
	require.Empty(t, l.unclaimed)
}

// TestLiveLogsUnclaimedBounded: the pairs remembered as imported unclaimed
// are bounded, oldest first out, and the ones whose display text the
// frontend may have dropped are forgotten.
func TestLiveLogsUnclaimedBounded(t *testing.T) {
	l := newLiveLogs(nil, "trace", &bodyCollector{}, true)
	lines := diagLines(l)
	unknown := map[uint16]bool{}
	importRoutes(l, unknown)
	read := importer(t, l)
	var rows []liveLogRow
	for n := uint16(1); n <= liveLogsUnclaimedMax+1; n++ {
		unknown[n] = true
		switch n {
		case 1:
		case 4:
			rows = append(rows, liveLogRow{span: n, body: "x", digest: "xxh3:out"})
		default:
			rows = append(rows, liveLogRow{span: n, body: "x"})
		}
	}
	read(0, liveLogRow{span: 1, body: "oldest"})
	read(0, rows...)
	require.Len(t, l.unclaimed, liveLogsUnclaimedMax)
	require.Len(t, l.unclaimedOrder, liveLogsUnclaimedMax)
	// The digest's records may route to the dropped span.
	l.release(idtui.LogRelease{Dropped: []dagui.SpanID{liveTestSpan(3)}})
	require.Len(t, l.unclaimed, liveLogsUnclaimedMax-2)
	clear(unknown)
	read(4, liveLogRow{span: 1, body: "oldest"}, liveLogRow{span: 3, body: "x"}, liveLogRow{span: 4, body: "x", digest: "xxh3:out"})
	require.Empty(t, lines(), "warned about a pair forgotten")
	read(4, liveLogRow{span: 2, body: "x"})
	require.Len(t, lines(), 1)
	require.Contains(t, lines()[0], "live logs: replay span "+liveTestSpan(2).String())
}

// TestLiveLogsReplayFollowed: in runUnplacedReply's scenario, the reply's
// first rows render twice, and the warning names them, and the opened check
// followed with its descendants, whose query returned them unplaced.
func TestLiveLogsReplayFollowed(t *testing.T) {
	var lines func() []string
	reply, check := liveTestSpan(7).String(), liveTestSpan(5).String()
	exported, _ := runUnplacedReply(t, func(l *liveLogs, fe idtui.LiveLogsRunningFrontend) {
		lines = diagLines(l)
		l.running = fe.LiveLogsRunning
	})
	require.Equal(t, 2, strings.Count(strings.Join(exported, ""), "Hello there"), "the scenario no longer replays: %q", exported)
	got := lines()
	require.NotEmpty(t, got)
	for _, line := range got {
		require.True(t, strings.HasPrefix(line, "live logs: replay span "+reply+" "), "%q", got)
	}
	require.Contains(t, got[0], "unclaimedRecords 1 unclaimedRead 0 claimedRecords 1 claimedRead ")
	require.Regexp(t, `rollUps (\w+,)*`+check+`\b`, got[0])
}

// runUnplacedReply drives an agent conversation through the running headless
// frontend and an engine-like store. The user opens a check the agent's tool
// call runs, which rolls up its exec's output: it is requested with its
// descendants, so while it runs, the follow's query has a tree, and the store
// also returns rows of spans it has no snapshot of. The reply's first rows
// are stored, and read, before its span reaches the store and the frontend;
// once it arrives, the conversation requests it, own logs only. setup
// configures the reader before it starts. It returns what the reader
// exported and the final render.
func runUnplacedReply(t *testing.T, setup func(*liveLogs, idtui.LiveLogsRunningFrontend)) (exported []string, rendered string) {
	t.Helper()
	t.Setenv("DAGGER_TUI_CONSOLE", "127.0.0.1:0")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
	const (
		rootN uint16 = iota + 1
		loopN
		promptN
		toolN
		checkN
		execN
		replyN
	)
	root, loop, prompt, tool, check, exec, reply := liveTestSpan(rootN), liveTestSpan(loopN), liveTestSpan(promptN),
		liveTestSpan(toolN), liveTestSpan(checkN), liveTestSpan(execN), liveTestSpan(replyN)
	fe := idtui.NewWithDB(io.Discard, dagui.NewDB())
	feCtx, stopFE := context.WithCancel(context.Background())
	feDone, ready := make(chan struct{}), make(chan struct{})
	var feErr error
	go func() {
		defer close(feDone)
		feErr = fe.Run(feCtx, dagui.FrontendOpts{}, func(ctx context.Context) (cleanups.CleanupF, error) {
			close(ready)
			<-ctx.Done()
			return nil, nil
		})
	}()
	<-ready
	lCtx, stopLogs := context.WithCancel(ctx)
	logsDone := make(chan struct{})
	t.Cleanup(func() {
		stopLogs()
		<-logsDone
		stopFE()
		<-feDone
	})

	store := &fakeLogStore{placing: true}
	store.snapshot(rootN, 0)
	store.snapshot(loopN, rootN)
	store.snapshot(promptN, loopN)
	store.snapshot(toolN, loopN)
	store.snapshot(checkN, toolN)
	store.snapshot(execN, checkN)
	store.add(
		liveLogRow{span: promptN, body: "Lint the repo\n"},
		liveLogRow{span: execN, body: "linting...\n"},
	)
	out := &bodyCollector{}
	l := newLiveLogs(store, live.String(), nil, true)
	unknown := make(chan struct{})
	var unknownOnce sync.Once
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogRoute {
		routes := fe.LiveLogsCoverage(targets)
		for i, target := range targets {
			if target.Span == reply && i < len(routes) && routes[i].Coverage == idtui.LogUnknown {
				unknownOnce.Do(func() { close(unknown) })
			}
		}
		return routes
	}
	l.activity = fe.LiveLogsActivity
	if setup != nil {
		setup(l, fe)
	}
	export := fe.SetLiveLogs(live, l.subscribe)
	l.export = exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
		if err := out.Export(ctx, records); err != nil {
			return err
		}
		return export.Export(ctx, records)
	})
	go func() { defer close(logsDone); l.run(lCtx) }()

	start := time.Now()
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: root, TraceID: live, Name: "agent", StartTime: start},
		{ID: loop, TraceID: live, ParentID: root, Name: "loop", StartTime: start, Agent: true, AgentID: "agent-1", AgentName: "agent"},
		{ID: prompt, TraceID: live, ParentID: loop, Name: "prompt", LLMRole: "user", Message: "sent", StartTime: start.Add(time.Millisecond), EndTime: start.Add(2 * time.Millisecond), HasLogs: true},
		{ID: tool, TraceID: live, ParentID: loop, Name: "Check", LLMRole: "assistant", LLMTool: "Check", Boundary: true, RollUpLogs: true, RollUpSpans: true, StartTime: start.Add(3 * time.Millisecond), HasLogs: true},
		{ID: check, TraceID: live, ParentID: tool, Name: "check lint", CheckName: "lint", RollUpLogs: true, StartTime: start.Add(4 * time.Millisecond)},
		{ID: exec, TraceID: live, ParentID: check, Name: "exec lint", StartTime: start.Add(5 * time.Millisecond), HasLogs: true},
	})
	fe.SetPrimary(root)
	fe.RequestSurfacedLogs()
	// The user opens the running check, and goes back to the conversation.
	fe.ZoomToSpan(check)
	fe.ZoomToSpan(root)
	fe.RequestSurfacedLogs()
	require.Eventually(t, func() bool {
		seen := store.seen()
		if len(seen) == 0 || !seen[len(seen)-1].Follow {
			return false
		}
		return slices.ContainsFunc(seen[len(seen)-1].Spans, func(s archive.LogSpan) bool {
			return s.SpanID == check.String() && s.Descendants
		})
	}, 5*time.Second, 10*time.Millisecond, "the opened check is not followed with its descendants")

	// The reply streams its first rows before its span reaches the store,
	// or the frontend: the follow reads them as rows of an unplaced span.
	store.add(liveLogRow{span: replyN, body: "Hello there, "})
	select {
	case <-unknown:
	case <-ctx.Done():
		t.Fatal("the follow never read the unplaced reply's rows")
	}
	// Its span arrives, and the conversation requests its own logs.
	store.snapshot(replyN, loopN)
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: reply, TraceID: live, ParentID: loop, Name: "reply", LLMRole: "assistant", Message: "received", StartTime: start.Add(10 * time.Millisecond), HasLogs: true},
	})
	fe.RequestSurfacedLogs()
	store.add(liveLogRow{span: replyN, body: "general Kenobi\n"})
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(out.got(), ""), "general Kenobi")
	}, 10*time.Second, 10*time.Millisecond, "the reply's later rows never arrived")
	require.NoError(t, fe.WaitForEventLoop(ctx))
	stopLogs()
	<-logsDone
	stopFE()
	<-feDone
	require.NoError(t, feErr)

	var final strings.Builder
	require.NoError(t, fe.FinalRender(&final))
	return out.got(), final.String()
}

// TestLiveLogsDiagnosticsRecords: the warnings are Warn records of their own
// internal span, started with the first, emitted through the command's
// logger provider -- the one whose processors carry the CLI's telemetry to
// the engine's store and Cloud -- not records of the primary span, whose
// output is printed at exit. A session with nothing to warn about records
// nothing.
func TestLiveLogsDiagnosticsRecords(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	var records recordCollector
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(&records)))
	ctx, root := tp.Tracer("test").Start(telemetry.WithLoggerProvider(t.Context(), lp), "command")
	defer root.End()

	clean := startLiveLogsDiag(ctx)
	clean.finish()
	require.Empty(t, records.records)
	require.Empty(t, spans.Ended(), "a clean session started the diagnostics span")

	d := startLiveLogsDiag(ctx)
	d.emit("replay", "span", "x")
	d.finish()
	d.emit("after finish")

	require.Len(t, records.records, 1)
	rec := records.records[0]
	require.Contains(t, rec.Body().AsString(), "WRN")
	require.Contains(t, rec.Body().AsString(), "live logs: replay")
	require.Equal(t, *d.span.Load(), rec.SpanID(), "not a record of the diagnostics span")
	require.NotEqual(t, root.SpanContext().SpanID(), rec.SpanID())
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		require.NotEqual(t, telemetry.StdioStreamAttr, kv.Key, "diagnostics are not stdio")
		return true
	})
	ended := spans.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, "live logs diagnostics", ended[0].Name())
	require.Equal(t, root.SpanContext().SpanID(), ended[0].Parent().SpanID())
	require.True(t, slices.ContainsFunc(ended[0].Attributes(), func(kv attribute.KeyValue) bool {
		return kv.Key == telemetry.UIInternalAttr && kv.Value.AsBool()
	}), "the diagnostics span is not internal: the TUI would render it")
}

// TestLiveLogsDiagnosticsRateLimit: a burst of warnings is capped per
// minute, and the next minute's first line, or finish, says how many were
// dropped.
func TestLiveLogsDiagnosticsRateLimit(t *testing.T) {
	var lines [][]any
	d := newLiveLogsDiag(func(msg string, args ...any) { lines = append(lines, append([]any{msg}, args...)) })
	clock := &fakeClock{t: time.Unix(100, 0)}
	d.now = clock.now
	for range 3 * liveLogsDiagRate {
		d.emit("replay")
	}
	require.Len(t, lines, liveLogsDiagRate)
	clock.advance(time.Minute)
	d.emit("replay")
	require.Equal(t, []any{"live logs: replay", "dropped", 2 * liveLogsDiagRate}, lines[len(lines)-1])
	for range liveLogsDiagRate {
		d.emit("replay")
	}
	d.finish()
	require.Len(t, lines, 2*liveLogsDiagRate+1)
	require.Equal(t, []any{"live logs: dropped", "dropped", 1}, lines[len(lines)-1])
}

// TestLiveLogsLongRollUp: a span followed with its descendants for over
// liveLogsLongRollUp is warned about once, with the work keeping it active.
func TestLiveLogsLongRollUp(t *testing.T) {
	l := newLiveLogs(nil, "trace", nil, true)
	lines := diagLines(l)
	clock := &fakeClock{t: time.Unix(100, 0)}
	l.now = clock.now
	l.diag.now = clock.now
	spawn, worker := liveTestSpan(5), liveTestSpan(8)
	var asked [][]dagui.SpanID
	l.running = func(ids []dagui.SpanID) []idtui.LogRunning {
		asked = append(asked, ids)
		return []idtui.LogRunning{{Total: 1, Running: []idtui.LogRunningSpan{{ID: worker, Name: "agent: treesitter", Agent: true}}}}
	}
	l.mu.Lock()
	l.spans[spawn.String()] = archive.LogSpan{SpanID: spawn.String(), Descendants: true}
	l.spans[liveTestSpan(3).String()] = archive.LogSpan{SpanID: liveTestSpan(3).String()}
	l.mu.Unlock()
	l.reportLongRollUps()
	clock.advance(liveLogsLongRollUp - time.Second)
	l.reportLongRollUps()
	require.Empty(t, asked, "asked about a span followed briefly")
	clock.advance(2 * time.Second)
	l.reportLongRollUps()
	l.reportLongRollUps()
	require.Equal(t, [][]dagui.SpanID{{spawn}}, asked, "reported more than once")
	require.Equal(t, []string{
		"live logs: long-lived roll-up span " + spawn.String() + " followedFor 10m1s self false running 1 agent true effects false runningSpans " +
			worker.String() + ` "agent: treesitter" [agent]`,
	}, lines())

	// Followed again, it is timed again.
	l.mu.Lock()
	delete(l.spans, spawn.String())
	l.mu.Unlock()
	l.reportLongRollUps()
	require.Empty(t, l.rollUps)
}
