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

// TestServiceRootFilterUsesTraceUI covers startup fallback, service headers,
// and navigation through the regular trace frontend rather than a command view.
func TestServiceRootFilterUsesTraceUI(t *testing.T) {
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

	install := func(db *dagui.DB) (*frontendPretty, string) {
		db.SetPrimarySpan(servicesID)
		fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
		fe.FrontendOpts.RootFilter = (*dagui.DB).ServiceDisplaySpans
		fe.FrontendOpts.ZoomedSpan = servicesID
		fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
		fe.setupTUI()
		fe.recalculateViewLocked()
		rendered := strings.Join(fe.tui.RenderLines(), "\n")
		return fe, rendered
	}

	// Before any display span exists: fall back to root's children, so the
	// screen shows setup progress instead of nothing.
	db := dagui.NewDB()
	db.ImportSnapshots(base)
	_, rendered := install(db)
	if !strings.Contains(rendered, "Workspace.services") {
		t.Fatalf("pre-service render lost setup progress:\n%s", rendered)
	}

	// With a display span: lead with it, hoisted past UpGroup.run, and drop
	// the setup noise.
	db = dagui.NewDB()
	db.ImportSnapshots(append(append([]dagui.SpanSnapshot{}, base...), service...))
	fe, rendered := install(db)
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
	if len(fe.rows.Order) != 1 || fe.rows.Order[0].Span.ID != displayID {
		t.Fatalf("regular trace rows = %+v, want the service display row", fe.rows.Order)
	}
	if fe.commandView != nil {
		t.Fatal("services installed a custom command view")
	}
	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			fe.tui.Inject(tuist.ParseKey(k))
			fe.tui.Step()
		}
	}

	// Search must use the displayed tree and reveal a collapsed descendant.
	press("/")
	if !fe.searchActive {
		t.Fatal("/ did not open search")
	}
	fe.confirmSearch("ready")
	fe.tui.RenderLines()
	if len(fe.searchMatches) != 1 || fe.FocusedSpan != readyID {
		t.Fatalf("search matches = %+v, focus = %s, want ready span", fe.searchMatches, fe.FocusedSpan)
	}
	press("esc", "home", "space")
	if !fe.autoFocus || fe.FocusedSpan != readyID {
		t.Fatalf("space: follow = %v, focus = %s, want ready span", fe.autoFocus, fe.FocusedSpan)
	}
	newID := prettyTestSpanID(9)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: newID, TraceID: prettyTestTraceID(), ParentID: displayID, Name: "new output", StartTime: at(6)},
	})
	fe.recalculateViewLocked()
	fe.Update()
	fe.tui.RenderLines()
	if fe.FocusedSpan != newID {
		t.Fatalf("follow after telemetry update = %s, want %s", fe.FocusedSpan, newID)
	}

	// Enter zooms; Escape restores the selected service roots.
	press("home", "enter")
	if fe.ZoomedSpan != displayID || fe.rows.BySpan[readyID] == nil || fe.rows.BySpan[displayID] != nil {
		t.Fatalf("zoom did not show the service's ordinary subtree: zoom = %s", fe.ZoomedSpan)
	}
	press("esc")
	if fe.ZoomedSpan != servicesID || fe.rows.BySpan[displayID] == nil {
		t.Fatal("Escape did not restore the service roots")
	}

	var final strings.Builder
	if err := fe.FinalRender(&final); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final.String(), "hello:web :80") || !strings.Contains(final.String(), "http://localhost:80") {
		t.Fatalf("final output lost service header: %s", final.String())
	}

	// Selecting roots must not reshape the shared DB for another frontend.
	ordinary := db.RowsView(dagui.FrontendOpts{ZoomedSpan: servicesID})
	if ordinary.BySpan[runID] == nil || ordinary.BySpan[loadID] == nil {
		t.Fatal("service root filter changed the underlying trace")
	}
}
