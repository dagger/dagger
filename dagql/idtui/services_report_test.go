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
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.servicesReport(tuist.Context{Width: 120}, r, false)
	if len(lines) == 0 {
		t.Fatal("servicesReport returned no lines")
	}
	got := strings.Join(lines, "\n")
	if !strings.HasPrefix(lines[0], "SERVICES") {
		t.Fatalf("top header = %q, want SERVICES heading\n%s", lines[0], got)
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
