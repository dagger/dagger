package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestServicesReportSurfacesInstances covers the reveal-independent SERVICES
// section: service-instance spans (running, exited, and failed) surface in the
// final report with their hostname, their command line (the exec span's own
// name), their state, and their span handle -- rendered after the main rows,
// never in place of them.
func TestServicesReportSurfacesInstances(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	runningID := prettyTestSpanID(3)
	exitedID := prettyTestSpanID(4)
	failedID := prettyTestSpanID(5)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "call",
			StartTime: start,
			// still running: an ended root would cancel the running service on
			// import (integrate marks left-running spans canceled)
		},
		{
			ID:          runningID,
			TraceID:     prettyTestTraceID(),
			Name:        "exec dagger-entrypoint.sh",
			Service:     true,
			ServiceName: "db.dagger.local",
			ParentID:    rootID,
			StartTime:   start.Add(time.Second),
			// no EndTime: still running
		},
		{
			ID:          exitedID,
			TraceID:     prettyTestTraceID(),
			Name:        "exec web-entrypoint.sh",
			Service:     true,
			ServiceName: "web.dagger.local",
			ParentID:    rootID,
			StartTime:   start.Add(2 * time.Second),
			EndTime:     start.Add(5 * time.Second),
			Final:       true,
		},
		{
			ID:          failedID,
			TraceID:     prettyTestTraceID(),
			Name:        "exec crash-entrypoint.sh",
			Service:     true,
			ServiceName: "bad.dagger.local",
			ParentID:    rootID,
			StartTime:   start.Add(3 * time.Second),
			EndTime:     start.Add(4 * time.Second),
			Status:      sdktrace.Status{Code: codes.Error, Description: "exit code: 1"},
			Final:       true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	// This section is agent-only, and span handles are part of that rendering
	// contract. Do not make the test depend on the caller's ambient environment.
	fe.FrontendOpts.AgentStyle = true
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.servicesReport(tuist.Context{Width: 120}, r, false)
	if len(lines) == 0 {
		t.Fatal("servicesReport returned no lines")
	}
	got := strings.Join(lines, "\n")
	if lines[0] != "== SERVICES ==" {
		t.Fatalf("top header = %q, want agent-style SERVICES heading\n%s", lines[0], got)
	}

	lineWith := func(substr string) string {
		for _, line := range strings.Split(got, "\n") {
			if strings.Contains(line, substr) {
				return line
			}
		}
		t.Fatalf("no line contains %q:\n%s", substr, got)
		return ""
	}

	runningLine := lineWith("db.dagger.local")
	if !strings.Contains(runningLine, "RUNNING") {
		t.Fatalf("running service line = %q, want RUNNING", runningLine)
	}
	if !strings.Contains(runningLine, "exec dagger-entrypoint.sh") {
		t.Fatalf("running service line = %q, want its command line (exec dagger-entrypoint.sh)", runningLine)
	}
	if !strings.Contains(runningLine, "span="+runningID.String()) {
		t.Fatalf("running service line = %q, want its span handle", runningLine)
	}
	if line := lineWith("web.dagger.local"); !strings.Contains(line, "EXITED") {
		t.Fatalf("exited service line = %q, want EXITED", line)
	}
	if line := lineWith("bad.dagger.local"); !strings.Contains(line, "ERROR") {
		t.Fatalf("failed service line = %q, want ERROR", line)
	}
}

// TestPromoteServicesGatedOnServicesPrimary covers the live-tree promotion
// for runs that are ABOUT their services (`dagger up`). Service spans are
// ambient -- any run that binds a service has one -- so without the command's
// declaration (SetServicesPrimary) the tree must stay untouched; with it, the
// zoomed span leads with each service's display span, auto-expanded to its
// readiness marker.
func TestPromoteServicesGatedOnServicesPrimary(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	build := func() (*dagui.DB, dagui.SpanID, dagui.SpanID, dagui.SpanID) {
		db := dagui.NewDB()
		rootID := prettyTestSpanID(1)
		servicesID := prettyTestSpanID(2)
		loadID := prettyTestSpanID(3)
		runID := prettyTestSpanID(4)
		displayID := prettyTestSpanID(5)
		starterID := prettyTestSpanID(6)
		execID := prettyTestSpanID(7)
		readyID := prettyTestSpanID(8)
		start := time.Unix(100, 0)
		at := func(n int) time.Time { return start.Add(time.Duration(n) * time.Second) }
		db.ImportSnapshots([]dagui.SpanSnapshot{
			// the CLI root; still running (an ended root would cancel the
			// running service on import)
			{ID: rootID, TraceID: prettyTestTraceID(), Name: "dagger up web", StartTime: start},
			// the CLI's `services` zoom span: passthrough, set primary --
			// exactly what internal/cmd/dagger/up.go's runServices creates
			{ID: servicesID, TraceID: prettyTestTraceID(), ParentID: rootID, Name: "services", Passthrough: true, StartTime: at(1)},
			// setup noise that promotion must hide
			{ID: loadID, TraceID: prettyTestTraceID(), ParentID: servicesID, Name: "Workspace.services", StartTime: at(1), EndTime: at(2), Final: true},
			{ID: runID, TraceID: prettyTestTraceID(), ParentID: servicesID, Name: "UpGroup.run", Passthrough: true, StartTime: at(2)},
			// the per-service display span RunUp starts
			{ID: displayID, TraceID: prettyTestTraceID(), ParentID: runID, Name: "hello:web :80", ServiceName: "hello:web", RollUpLogs: true, StartTime: at(3)},
			{ID: starterID, TraceID: prettyTestTraceID(), ParentID: displayID, Name: "service.start", Passthrough: true, StartTime: at(4)},
			{ID: execID, TraceID: prettyTestTraceID(), ParentID: starterID, Name: "exec nginx", Service: true, ServiceName: "web.dagger.local", Passthrough: true, StartTime: at(4)},
			{ID: readyID, TraceID: prettyTestTraceID(), ParentID: displayID, Name: "ready http://localhost:80", ServiceURLs: []string{"http://localhost:80"}, StartTime: at(5)},
		})
		db.SetPrimarySpan(servicesID)
		return db, servicesID, displayID, readyID
	}

	// Without the declaration: no promotion, no reveals -- the ordinary tree.
	db, servicesID, _, _ := build()
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	if got := db.Spans.Map[servicesID].RevealedSpans.Order; len(got) != 0 {
		t.Fatalf("undeclared run revealed spans on the primary: %+v", got)
	}

	// With it: the zoomed span leads with the display span, expanded to its
	// readiness marker, and the setup noise is gone.
	db, servicesID, displayID, readyID := build()
	fe = NewWithDB(io.Discard, db)
	fe.servicesPrimary = true
	fe.ZoomedSpan = servicesID
	fe.recalculateViewLocked()
	if got := db.Spans.Map[servicesID].RevealedSpans.Order; len(got) != 1 || got[0].ID != displayID {
		t.Fatalf("promoted revealed spans = %+v, want just the display span", got)
	}
	rows := fe.rows.Order
	if len(rows) != 2 {
		names := make([]string, len(rows))
		for i, row := range rows {
			names[i] = row.Span.Name
		}
		t.Fatalf("rows = %v, want [display ready]", names)
	}
	if rows[0].Span.ID != displayID || rows[0].Depth != 0 || !rows[0].Expanded {
		t.Fatalf("row 0 = %q depth=%d expanded=%v, want the auto-expanded display span at the top level",
			rows[0].Span.Name, rows[0].Depth, rows[0].Expanded)
	}
	if rows[1].Span.ID != readyID || rows[1].Depth != 1 {
		t.Fatalf("row 1 = %q depth=%d, want the readiness marker beneath the display span",
			rows[1].Span.Name, rows[1].Depth)
	}
}
