package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// TestTraceReportGuardPassesThroughSmallReports verifies the common case: the
// measured real-world reports (59 B for a single check, ~2 KB scoped) must
// reach the caller byte-identical.
func TestTraceReportGuardPassesThroughSmallReports(t *testing.T) {
	for _, report := range []string{
		"",
		"CHECKS: 1 passed\n",
		"◼ check shellcheck:check\n┃ all good\n\nCHECKS: 1 passed\n\nRUN LOCALLY\n  dagger check\n",
		strings.Repeat("◼ a nested span with a unicode bullet\n", 100),
	} {
		if got := guardTraceReport(report); got != report {
			t.Fatalf("guardTraceReport modified an under-budget report:\ngot  %q\nwant %q", got, report)
		}
	}
}

// TestTraceReportGuardClampsLongLines covers the module-heavy case, where a
// single verbatim `schema(json: "…")` argument or `.contents: JSON!` result
// dwarfs the rest of the report.
func TestTraceReportGuardClampsLongLines(t *testing.T) {
	long := "schema(json: \"" + strings.Repeat("x", 5000) + "\")"
	report := "head\n" + long + "\ntail\n"

	got := guardTraceReport(report)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected line boundaries preserved, got %d lines: %q", len(lines), got)
	}
	if lines[0] != "head" || lines[2] != "tail" {
		t.Fatalf("expected surrounding lines untouched, got %q", got)
	}
	clamped := lines[1]
	if !strings.HasPrefix(clamped, long[:traceReportMaxLineLen]) {
		t.Fatalf("expected clamped line to keep its first %d bytes, got %q", traceReportMaxLineLen, clamped)
	}
	if !strings.Contains(clamped, "bytes truncated]") {
		t.Fatalf("expected an inline truncation marker, got %q", clamped)
	}
	if len(clamped) >= len(long) {
		t.Fatalf("expected clamped line to be shorter than the original (%d >= %d)", len(clamped), len(long))
	}
}

// TestTraceReportGuardClampsOnRuneBoundary makes sure the clamp never splits a
// multi-byte rune -- the report is full of box-drawing characters.
func TestTraceReportGuardClampsOnRuneBoundary(t *testing.T) {
	// "┃" is 3 bytes, so a run of them straddles the 2000-byte clamp.
	line := strings.Repeat("┃", traceReportMaxLineLen)
	got := clampLineBytes(line, traceReportMaxLineLen)
	// Everything before the marker must be valid UTF-8 and within the clamp.
	idx := strings.Index(got, "[... ")
	if idx < 0 {
		t.Fatalf("expected a truncation marker, got %q", got[:min(80, len(got))])
	}
	kept := got[:idx]
	if !utf8.ValidString(kept) {
		t.Fatalf("clamp split a rune: kept portion is not valid UTF-8")
	}
	if len(kept) > traceReportMaxLineLen {
		t.Fatalf("kept %d bytes, over the %d-byte clamp", len(kept), traceReportMaxLineLen)
	}
	if len(kept) < traceReportMaxLineLen-utf8.UTFMax {
		t.Fatalf("clamp gave up too much: kept %d of %d bytes", len(kept), traceReportMaxLineLen)
	}
}

// TestTraceReportGuardTruncatesMiddle covers the total-budget blowout: the
// span tree at the head and the summary sections at the tail both carry
// signal, so the middle is what goes.
func TestTraceReportGuardTruncatesMiddle(t *testing.T) {
	var b strings.Builder
	b.WriteString("◼ FIRST LINE OF THE SPAN TREE\n")
	for i := range 5000 {
		fmt.Fprintf(&b, "◼ span number %d doing some work\n", i)
	}
	b.WriteString("CHECKS: 42 passed\n")
	b.WriteString("RUN LOCALLY: dagger check\n")
	report := b.String()

	got := guardTraceReport(report)
	if len(got) > traceReportMaxBytes {
		t.Fatalf("guarded report is %d bytes, over the %d-byte budget", len(got), traceReportMaxBytes)
	}
	if !strings.HasPrefix(got, "◼ FIRST LINE OF THE SPAN TREE\n") {
		t.Fatalf("expected the head to be kept, got %q", got[:min(80, len(got))])
	}
	if !strings.HasSuffix(got, "CHECKS: 42 passed\nRUN LOCALLY: dagger check\n") {
		t.Fatalf("expected the tail to be kept, got %q", got[max(0, len(got)-80):])
	}
	if !strings.Contains(got, "omitted from the middle of this report") {
		t.Fatalf("expected a truncation marker, got %q", got[:min(200, len(got))])
	}

	// Line boundaries survive: every kept line is a whole line of the input.
	inputLines := map[string]bool{}
	for _, line := range strings.Split(report, "\n") {
		inputLines[line] = true
	}
	var markers int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "omitted from the middle") {
			markers++
			continue
		}
		if !inputLines[line] {
			t.Fatalf("kept line is not a whole input line: %q", line)
		}
	}
	if markers != 1 {
		t.Fatalf("expected exactly one truncation marker, got %d", markers)
	}

	// Both halves are generous: neither end is a token gesture.
	head, _, ok := strings.Cut(got, "... ")
	if !ok {
		t.Fatal("expected to find the marker")
	}
	if len(head) < traceReportMaxBytes/2 {
		t.Fatalf("head is only %d bytes of a %d-byte budget", len(head), traceReportMaxBytes)
	}
}

// TestExpandedSpansUnwrapsToFirstRealWork covers the narrow unwrap a scoped
// tool-call report needs. Force-expanding the WHOLE subtree (what this used to
// do) punches open every roll-up boundary in it -- module-internal glob
// matching, long CACHED call chains with verbatim arguments -- which measured
// 713 lines over the 16 KiB report budget for a single check.
//
// What actually has to be forced open is the path from the tool-call span
// (LLMTool + roll-ups, which TraceTree.IsExpanded would otherwise collapse to a
// bare status line) down to the tool's own work. Everything below that is left
// to the normal rules.
func TestExpandedSpansUnwrapsToFirstRealWork(t *testing.T) {
	const (
		toolID byte = iota + 1
		queryID
		profID
		workID
		nestedID
		deepID
	)
	start := time.Unix(100, 0)
	snap := func(id byte, name string, parent byte) dagui.SpanSnapshot {
		s := dagui.SpanSnapshot{
			ID:        traceTargetSpanID(id),
			TraceID:   dagui.TraceID{TraceID: trace.TraceID{1}},
			Name:      name,
			StartTime: start,
			EndTime:   start.Add(time.Second),
			Final:     true,
		}
		if parent != 0 {
			s.ParentID = traceTargetSpanID(parent)
		}
		return s
	}

	tool := snap(toolID, "check", 0)
	tool.LLMTool = "check"
	tool.Boundary = true
	tool.RollUpLogs = true
	tool.RollUpSpans = true

	// A pure API frame, then the module-function profiling twin (no logs, one
	// child): both are wrappers on the way to the work.
	query := snap(queryID, "POST /query", toolID)
	prof := snap(profID, "dagger-dev:Workspace.check", queryID)

	// The tool's own work: it rolls up its logs, which is exactly the boundary
	// the unwrap must punch through so its printed output survives.
	work := snap(workID, "Workspace.check", profID)
	work.RollUpLogs = true

	// Nested work below it: another roll-up, which must stay closed.
	nested := snap(nestedID, "Container.withExec", workID)
	nested.RollUpSpans = true
	deep := snap(deepID, "Directory.glob", nestedID)

	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{tool, query, prof, work, nested, deep})

	got := expandedSpans(db, traceTargetSpanID(toolID))
	for _, want := range []byte{toolID, queryID, profID, workID} {
		if !got[traceTargetSpanID(want)] {
			t.Errorf("span %d should be force-expanded, got %v", want, got)
		}
	}
	for _, unwanted := range []byte{nestedID, deepID} {
		if got[traceTargetSpanID(unwanted)] {
			t.Errorf("span %d must be left to the normal expansion rules, got %v", unwanted, got)
		}
	}

	// Without a scope there is no tool-call boundary to punch through.
	if unscoped := expandedSpans(db, dagui.SpanID{}); len(unscoped) != 0 {
		t.Errorf("an unscoped render must not force anything open, got %v", unscoped)
	}
}

// TestToolCallReportOptsHideTreeButReadTraceKeepsIt pins the one difference
// between the two LLM-facing report shapes: a tool result carries only what
// its call surfaced, while ReadTrace -- whose whole purpose is showing the
// shape of what ran -- keeps the span tree.
func TestToolCallReportOptsHideTreeButReadTraceKeepsIt(t *testing.T) {
	if !toolCallReportOpts().HideSpanTree {
		t.Error("a tool call's own report must not render the span tree")
	}
	if readTraceReportOpts().HideSpanTree {
		t.Error("ReadTrace must keep the span tree")
	}
	require.True(t, readTraceReportOpts().OwnOutputOnly)
	require.True(t, readTraceReportOpts().ExpandWrappers)
}

func TestTraceReportHidesInternalSpans(t *testing.T) {
	const (
		rootID byte = iota + 1
		internalID
		visibleID
	)
	start := time.Unix(100, 0)
	snap := func(id byte, name string, parent byte) dagui.SpanSnapshot {
		s := dagui.SpanSnapshot{
			ID:        traceTargetSpanID(id),
			TraceID:   dagui.TraceID{TraceID: trace.TraceID{1}},
			Name:      name,
			StartTime: start,
			EndTime:   start.Add(time.Second),
			Final:     true,
		}
		if parent != 0 {
			s.ParentID = traceTargetSpanID(parent)
		}
		return s
	}

	root := snap(rootID, "trace root", 0)
	internal := snap(internalID, "internal getter", rootID)
	internal.Internal = true
	visible := snap(visibleID, "visible work", rootID)

	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{root, internal, visible})
	session := idtui.NewReportSession(db)
	report, err := renderTraceReportSession(session, root.ID.String(), traceReportOpts{Scoped: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.body, internal.Name) {
		t.Fatalf("internal span should be hidden from trace report:\n%s", report.body)
	}
	if !strings.Contains(report.body, visible.Name) {
		t.Fatalf("visible span should remain in trace report:\n%s", report.body)
	}
}

func TestTraceFailureNavigationSurvivesDispatch(t *testing.T) {
	start := time.Unix(100, 0)
	snapshot := func(id byte, name string, parent byte) dagui.SpanSnapshot {
		return dagui.SpanSnapshot{
			ID: traceTargetSpanID(id), ParentID: traceTargetSpanID(parent),
			TraceID: dagui.TraceID{TraceID: trace.TraceID{1}}, Name: name,
			StartTime: start, EndTime: start.Add(time.Second), Final: true,
		}
	}
	root := snapshot(1, "run", 0)
	check := snapshot(2, "install", 1)
	check.CheckName = "sdk:installer:check"
	check.Status = sdktrace.Status{Code: codes.Error}
	failed := snapshot(3, "installer", 2)
	failed.TestCaseName = "SDK/Installer/checksum"
	failed.Boundary = true
	failed.TestStatus = dagui.TestStatusFailure
	failed.Status = sdktrace.Status{Code: codes.Error}
	origin := snapshot(4, "sha256sum --check", 0) // outside containment
	origin.Status = sdktrace.Status{Code: codes.Error}
	root.Status = sdktrace.Status{Code: codes.Error, Description: "runner boundary failed"}
	snaps := []dagui.SpanSnapshot{root, check, failed, origin}
	// Expected failed probes under successful work must never become origins.
	for i := byte(5); i < 120; i++ {
		success := snapshot(i, "successful operation", 3)
		probe := snapshot(i+120, "expected failed probe", i)
		probe.Status = sdktrace.Status{Code: codes.Error}
		snaps = append(snaps, success, probe)
	}
	for i := byte(240); i < 250; i++ {
		success := snapshot(i, "successful case "+strings.Repeat("x", 200), 2)
		success.TestCaseName = fmt.Sprintf("SDK/success/%d", i)
		success.TestStatus = dagui.TestStatusSuccess
		snaps = append(snaps, success)
	}
	teardown := snapshot(250, "late teardown", 3)
	teardown.TestCaseName = "SDK/Installer/checksum/teardown"
	teardown.TestStatus = dagui.TestStatusFailure
	teardown.Status = sdktrace.Status{Code: codes.Error, Description: "teardown assertion"}
	snaps = append(snaps, teardown)
	db := dagui.NewDB()
	db.ImportSnapshots(snaps)
	db.Spans.Map[root.ID].ErrorOrigins.Add(db.Spans.Map[origin.ID])
	expanded := failureReportExpansion(db, db.Spans.Map[root.ID])
	require.True(t, expanded[teardown.ID])
	require.False(t, expanded[traceTargetSpanID(5)])
	require.False(t, expanded[traceTargetSpanID(125)])
	focused, err := renderTraceReportSession(idtui.NewReportSession(db), root.ID.String(), readTraceReportOpts())
	require.NoError(t, err)
	require.NotContains(t, focused.body, "expected failed probe")
	require.Contains(t, focused.failures, "SDK/Installer/checksum/teardown")
	require.NotContains(t, focused.body, "FindSpans(")
	report, err := renderTraceReportSession(idtui.NewReportSession(db), root.ID.String(), toolCallReportOpts())
	require.NoError(t, err)
	require.Contains(t, report.failures, `test "SDK/Installer/checksum/teardown"`)
	require.Contains(t, report.failures, "teardown assertion")
	require.NotContains(t, report.failures, "expected failed probe")
	require.Contains(t, report.failures, `test "SDK/Installer/checksum"`)
	require.Contains(t, report.failures, `check "sdk:installer:check"`)
	require.NotContains(t, report.failures, "success")
	require.Equal(t, 1, strings.Count(report.failures, "origin "))
	require.LessOrEqual(t, len(report.failures), traceFailureMaxBytes)

	result := combineSpanResult(root.ID.String(), strings.Repeat("own output\n", 10000), report.body, report.failures)
	// Exercise the outer dispatch, not only the report guard. A large value
	// appended by the ordinary tool adapter forces that final guard as well.
	result += strings.Repeat("returned value\n", 10000)
	got := newMCP().CallContent(t.Context(), []LLMTool{{Name: "run", Call: func(context.Context, any) (any, error) {
		return result, nil
	}}}, &LLMToolCall{Name: "run"})
	require.False(t, got.Errored)
	require.True(t, strings.HasPrefix(got.Text, "== FAILURES =="))
	for _, span := range []dagui.SpanSnapshot{check, failed, origin} {
		require.Contains(t, got.Text, fmt.Sprintf("ReadTrace(span: %q)", span.ID.String()))
		require.Contains(t, got.Text, fmt.Sprintf("ReadLogs(span: %q)", span.ID.String()))
	}
	require.NotContains(t, got.Text, "use ReadLogs(span: "+root.ID.String()+")")

	// Names never consume the exact calls, even when both sections overflow.
	for _, span := range db.Spans.Order {
		span.CheckName = strings.Repeat("long", 1000)
		span.Status.Code = codes.Error
	}
	nav := traceFailureNavigation(db, db.Spans.Map[root.ID])
	require.LessOrEqual(t, len(nav), traceFailureMaxBytes)
	require.Contains(t, nav, "more check entries")
	require.Contains(t, nav, fmt.Sprintf("ReadLogs(span: %q)", origin.ID.String()))
}

func TestTraceFailureNavigationLoadsExternalOrigin(t *testing.T) {
	store, ids := traceInspectStore(t)
	const traceID = "000102030405060708090a0b0c0d0e0f"
	const checksumID = "00000000000000ff"
	_, err := store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: checksumID, Name: "checksum exec", StartTime: 100, EndTime: sql.NullInt64{Int64: 200, Valid: true},
			StatusCode: int64(codes.Error), Attributes: marshalSpanAttrs(t), Links: []byte("[]")},
		{TraceID: traceID, SpanID: ids["build"], Name: "failed installer", StartTime: 100, EndTime: sql.NullInt64{Int64: 200, Valid: true},
			StatusCode: int64(codes.Error), Attributes: marshalSpanAttrs(t),
			Links: marshalPurposeLink(t, traceID, checksumID, telemetry.LinkPurposeErrorOrigin)},
	})
	require.NoError(t, err)
	session, err := loadTraceReportSession(t.Context(), store, ids["build"])
	require.NoError(t, err)
	report, err := renderTraceReportSession(session, ids["build"], toolCallReportOpts())
	require.NoError(t, err)
	require.Contains(t, report.failures, `origin "checksum exec"`)
	require.Contains(t, report.failures, fmt.Sprintf("ReadLogs(span: %q)", checksumID))
}

// Fail a specific telemetry access after target resolution, so dispatch tests
// exercise the actual capture and render paths rather than mocked tool results.
type inspectionFailureServer struct {
	*logCaptureTestServer
	calls, failAt int
}

func (s *inspectionFailureServer) ClientTelemetry(ctx context.Context, session, client string) (*clientdb.DB, error) {
	s.calls++
	if s.calls == s.failAt {
		return nil, errors.New("telemetry unavailable")
	}
	return s.logCaptureTestServer.ClientTelemetry(ctx, session, client)
}

func TestInspectionErrorsThroughDispatch(t *testing.T) {
	const spanID = "0000000000000001"
	const traceID = "000102030405060708090a0b0c0d0e0f"
	for _, tc := range []struct {
		name, tool, want string
		failAt           int
	}{
		{"target store", "ReadTrace", "resolve trace target", 1},
		{"capture", "ReadTrace", "capture logs for span", 2},
		{"report", "ReadTrace", "render report for span", 3},
		{"log capture", "ReadLogs", "failed to capture logs", 1},
		{"log availability", "ReadLogs", "check log availability", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbs := clientdb.NewDBs(t.TempDir())
			store, err := dbs.Open(t.Context(), "capture-test")
			require.NoError(t, err)
			defer store.Close()
			_, err = store.AppendSpans([]clientdb.Span{{TraceID: traceID, SpanID: spanID, Name: "quiet", Attributes: []byte("[]"), Links: []byte("[]")}})
			require.NoError(t, err)
			if tc.tool == "ReadTrace" {
				_, err = store.AppendLogs([]clientdb.Log{persistedCaptureLog(t, traceID, spanID, "stdout", stringLogBody("partial output must not disguise failure\n"))})
				require.NoError(t, err)
			}
			srv := &inspectionFailureServer{logCaptureTestServer: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}, failAt: tc.failAt}
			ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
			m := newMCP()
			call := m.readTraceTool(&dagql.Server{})
			if tc.tool == "ReadLogs" {
				call = m.readLogsTool(&dagql.Server{})
			}
			got := m.CallContent(ctx, []LLMTool{{Name: tc.tool, Call: call}}, &LLMToolCall{Name: tc.tool, Arguments: JSON(`{"span":"` + spanID + `"}`)})
			require.True(t, got.Errored, got.Text)
			require.Contains(t, got.Text, tc.want)
			require.Contains(t, got.Text, "telemetry unavailable")
			require.NotContains(t, got.Text, "no trace report")
			require.NotContains(t, got.Text, "no logs beneath")
		})
	}
}

func TestInspectionArgumentErrorsThroughDispatch(t *testing.T) {
	m := newMCP()
	for _, args := range []string{
		`{}`,
		`{"check":"unit:check"}`,
		`{"test":"TestUnit","view":"inspect"}`,
		`{"test":"TestUnit","check":"unit:check","view":"timings"}`,
	} {
		got := m.CallContent(t.Context(), []LLMTool{{Name: "ReadTrace", Call: m.readTraceTool(&dagql.Server{})}},
			&LLMToolCall{Name: "ReadTrace", Arguments: JSON(args)})
		require.True(t, got.Errored, got.Text)
		require.Contains(t, got.Text, "required argument span not provided")
	}
	for _, tool := range []LLMTool{
		{Name: "ReadTrace", Call: m.readTraceTool(&dagql.Server{})},
		{Name: "ReadLogs", Call: m.readLogsTool(&dagql.Server{})},
	} {
		got := m.CallContent(t.Context(), []LLMTool{tool}, &LLMToolCall{Name: tool.Name, Arguments: JSON(`{"span":"invalid"}`)})
		require.True(t, got.Errored, got.Text)
		require.Contains(t, got.Text, "invalid span ID")
	}
}

func TestAutomaticDecorationRemainsBestEffort(t *testing.T) {
	const spanID = "0000000000000001"
	const traceID = "000102030405060708090a0b0c0d0e0f"
	dbs := clientdb.NewDBs(t.TempDir())
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	defer store.Close()
	_, err = store.AppendSpans([]clientdb.Span{{TraceID: traceID, SpanID: spanID, Name: "work", Attributes: []byte("[]"), Links: []byte("[]")}})
	require.NoError(t, err)
	_, err = store.AppendLogs([]clientdb.Log{persistedCaptureLog(t, traceID, spanID, "stdout", stringLogBody("own output\n"))})
	require.NoError(t, err)
	srv := &inspectionFailureServer{logCaptureTestServer: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}, failAt: 2}
	ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
	m := newMCP()
	got := m.CallContent(ctx, []LLMTool{{Name: "ordinary", Call: func(ctx context.Context, _ any) (any, error) {
		return "tool succeeded\n" + m.spanResult(ctx, spanID, toolCallReportOpts()), nil
	}}}, &LLMToolCall{Name: "ordinary"})
	require.False(t, got.Errored)
	require.Contains(t, got.Text, "tool succeeded")
	require.Contains(t, got.Text, "own output")
	require.NotContains(t, got.Text, "telemetry unavailable")

	// Failure decoration must keep the real tool error, not replace it with
	// the inspection error, even when neither telemetry component is available.
	original := errors.New("installer failed [traceparent:" + traceID + "-" + spanID + "]")
	got = m.CallContent(t.Context(), []LLMTool{{Name: "ordinary", Call: func(context.Context, any) (any, error) {
		return nil, original
	}}}, &LLMToolCall{Name: "ordinary"})
	require.True(t, got.Errored)
	require.Equal(t, original.Error(), got.Text)
}

func TestImportedFailureNavigationThroughTools(t *testing.T) {
	const (
		traceID  = "000102030405060708090a0b0c0d0e0f"
		rootID   = "0000000000000001"
		checkID  = "0000000000000002"
		testID   = "0000000000000003"
		originID = "0000000000000004"
	)
	dbs := clientdb.NewDBs(t.TempDir())
	live, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	defer live.Close()
	attr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: stringLogBody(value)}
	}
	_, err = live.ImportTrace(t.Context(), traceID, func(store *clientdb.DB) error {
		spans := []clientdb.Span{
			{TraceID: traceID, SpanID: rootID, Name: "SDK checks"},
			{TraceID: traceID, SpanID: checkID, ParentSpanID: validSpanID(rootID), Name: "installer check", StatusCode: int64(codes.Error),
				Attributes: marshalSpanAttrs(t, attr(telemetry.CheckNameAttr, "sdk:installer:check")),
				Links:      marshalPurposeLink(t, traceID, originID, telemetry.LinkPurposeErrorOrigin)},
			{TraceID: traceID, SpanID: testID, ParentSpanID: validSpanID(checkID), Name: "installer test", StatusCode: int64(codes.Error),
				Attributes: marshalSpanAttrs(t, attr("test.case.name", "SDK/Installer/checksum"), attr("test.status", "failure")),
				Links:      marshalPurposeLink(t, traceID, originID, telemetry.LinkPurposeErrorOrigin)},
			// Deliberately outside the root's containment; only the error-origin
			// second pass finds it. Its logs must be retrieved by its own ID.
			{TraceID: traceID, SpanID: originID, Name: "checksum exec", StatusCode: int64(codes.Error)},
		}
		logs := []clientdb.Log{persistedCaptureLog(t, traceID, originID, "stderr", stringLogBody("sha256sum: installer.tar.gz: FAILED\nexpected checksum did not match\n"))}
		for i := 5; i < 250; i++ {
			id := fmt.Sprintf("%016x", i)
			spans = append(spans, clientdb.Span{TraceID: traceID, SpanID: id, ParentSpanID: validSpanID(rootID), Name: fmt.Sprintf("success %d", i),
				Attributes: marshalSpanAttrs(t, attr("test.case.name", fmt.Sprintf("SDK/success/%d", i)), attr("test.status", "success"))})
			logs = append(logs, persistedCaptureLog(t, traceID, id, "stdout", stringLogBody(strings.Repeat("successful output\n", 30))))
		}
		for i := range spans {
			spans[i].StartTime = 100
			spans[i].EndTime = sql.NullInt64{Int64: 200, Valid: true}
			if spans[i].Attributes == nil {
				spans[i].Attributes = []byte("[]")
			}
			if spans[i].Links == nil {
				spans[i].Links = []byte("[]")
			}
			spans[i].Events = []byte("[]")
			spans[i].Resource = []byte("{}")
			spans[i].InstrumentationScope = []byte("{}")
		}
		if _, err := store.AppendSpans(spans); err != nil {
			return err
		}
		_, err := store.AppendLogs(logs)
		return err
	})
	require.NoError(t, err)
	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}})
	m := newMCP()
	tools := NewLLMToolSet()
	m.loadBuiltins(&dagql.Server{}, tools)
	root := m.CallContent(ctx, tools.Order, &LLMToolCall{Name: "ReadTrace", Arguments: JSON(`{"span":"` + rootID + `"}`)})
	require.False(t, root.Errored, root.Text)
	require.Contains(t, root.Text, `test "SDK/Installer/checksum"`)
	require.Contains(t, root.Text, `check "sdk:installer:check"`)
	require.Contains(t, root.Text, `origin "checksum exec"`)
	require.Contains(t, root.Text, fmt.Sprintf("ReadLogs(span: %q)", originID))
	require.NotContains(t, root.Text, "expected checksum did not match", "external origin evidence should be linked, not embedded")
	// Follow only the precise origin link, never the run's root logs.
	logs := m.CallContent(ctx, tools.Order, &LLMToolCall{Name: "ReadLogs", Arguments: JSON(`{"span":"` + originID + `"}`)})
	require.False(t, logs.Errored, logs.Text)
	require.Contains(t, logs.Text, "sha256sum: installer.tar.gz: FAILED")
	require.Contains(t, logs.Text, "expected checksum did not match")
	require.NotContains(t, logs.Text, "successful output")
}

func TestEmptyLogsDistinguishesHistorical(t *testing.T) {
	live, ids := traceInspectStore(t)
	text, err := emptyLogsResultIn(live, ids["test"])
	require.NoError(t, err)
	require.Contains(t, text, "yet")
	const historicalID = "00000000000000ff"
	_, err = live.ImportTrace(t.Context(), "history", func(store *clientdb.DB) error {
		_, err := store.AppendSpans([]clientdb.Span{{TraceID: "000102030405060708090a0b0c0d0e0f", SpanID: historicalID, Name: "old"}})
		return err
	})
	require.NoError(t, err)
	text, err = emptyLogsResultIn(live, historicalID)
	require.NoError(t, err)
	require.Contains(t, text, "no logs recorded")
	require.NotContains(t, text, "yet")
	_, err = emptyLogsResultIn(live, "00000000000000ee")
	require.ErrorContains(t, err, "not found")
}
