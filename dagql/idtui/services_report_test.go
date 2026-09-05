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

// TestServiceListLeadsWithDisplaySpans covers the command-owned row set for
// runs that are ABOUT their services (`dagger up`). Service spans are ambient
// -- any run that binds a service has one -- so nothing is inferred from span
// data: the rows only reshape when the command installs the view. Once the
// per-service display span exists, ServiceList hoists it to the top of the
// list -- however deep the call machinery that opened it -- with the setup
// noise passed through, and the collapsed row chips the ready URL. Until
// then, the list falls back to root's own children so setup progress shows.
func TestServiceListLeadsWithDisplaySpans(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
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
	base := []dagui.SpanSnapshot{
		// the CLI root; still running (an ended root would cancel the
		// running service on import)
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "dagger up web", StartTime: start},
		// the CLI's `services` zoom span: passthrough, set primary --
		// exactly what internal/cmd/dagger/up.go's runServices creates
		{ID: servicesID, TraceID: prettyTestTraceID(), ParentID: rootID, Name: "services", Passthrough: true, StartTime: at(1)},
		// setup machinery the service rows must not drown in
		{ID: loadID, TraceID: prettyTestTraceID(), ParentID: servicesID, Name: "Workspace.services", StartTime: at(1)},
		// the call span the display spans actually live under: NOT
		// passthrough, so leading with the services requires hoisting
		{ID: runID, TraceID: prettyTestTraceID(), ParentID: servicesID, Name: "UpGroup.run", StartTime: at(2)},
	}
	service := []dagui.SpanSnapshot{
		// the per-service display span PrepareUp opens, with the ready URL
		// stamped on it once the health check passed
		{ID: displayID, TraceID: prettyTestTraceID(), ParentID: runID, Name: "hello:web :80", ServiceName: "hello:web", RollUpLogs: true, ServiceURLs: []string{"http://localhost:80"}, StartTime: at(3)},
		{ID: starterID, TraceID: prettyTestTraceID(), ParentID: displayID, Name: "service.start", Passthrough: true, StartTime: at(4)},
		{ID: execID, TraceID: prettyTestTraceID(), ParentID: starterID, Name: "exec nginx", Service: true, ServiceName: "web.dagger.local", Passthrough: true, StartTime: at(4)},
		{ID: readyID, TraceID: prettyTestTraceID(), ParentID: displayID, Name: "ready http://localhost:80", ServiceURLs: []string{"http://localhost:80"}, StartTime: at(5)},
	}

	install := func(db *dagui.DB) (*frontendPretty, string, *SpanListView) {
		db.SetPrimarySpan(servicesID)
		fe := NewWithDB(io.Discard, db)
		fe.reportOnly = true
		view := &commandViewFixture{label: "up"}
		var list *SpanListView
		fe.SetView(func(ctx ViewContext) CommandView {
			list = ctx.ServiceList(func() dagui.SpanID { return servicesID })
			view.child = list
			return view
		})
		// The view factory runs on the first frame, so render before
		// returning the list it built.
		rendered := strings.Join(fe.tui.RenderLines(), "\n")
		return fe, rendered, list
	}

	// Before any display span exists: fall back to root's children, so the
	// screen shows setup progress instead of nothing.
	db := dagui.NewDB()
	db.ImportSnapshots(base)
	_, rendered, _ := install(db)
	if !strings.Contains(rendered, "Workspace.services") {
		t.Fatalf("pre-service render lost setup progress:\n%s", rendered)
	}

	// With a display span: lead with it, hoisted past UpGroup.run, and drop
	// the setup noise.
	db = dagui.NewDB()
	db.ImportSnapshots(append(append([]dagui.SpanSnapshot{}, base...), service...))
	fe, rendered, list := install(db)
	displayLine := ""
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "hello:web :80") {
			displayLine = line
			break
		}
	}
	if displayLine == "" {
		t.Fatalf("display span was not rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "Workspace.services") || strings.Contains(rendered, "UpGroup.run") {
		t.Fatalf("setup noise rendered alongside the services:\n%s", rendered)
	}
	// The display span rolls up its logs (RollUpLogs), so its row stays
	// collapsed -- the ready URL must be legible on the row itself.
	if !strings.Contains(displayLine, "http://localhost:80") {
		t.Fatalf("display row = %q, want the ready URL chip", displayLine)
	}
	if !list.FocusFirst() || fe.FocusedSpan != displayID {
		t.Fatalf("service list did not focus the display span: got %s, want %s", fe.FocusedSpan, displayID)
	}
}
