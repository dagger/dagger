package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// A failed check shows its own error, also when another test's failure sits
// beneath it: a test run shared by several checks sits under whichever one
// started it, so the tests beneath a check may not explain that check.
func TestChecksReportFailedCheckOwnError(t *testing.T) {
	const ownError = "TestBar did not run"
	rootID := prettyTestSpanID(1)
	checkID := prettyTestSpanID(2)
	testID := prettyTestSpanID(3)
	originID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	spans := func(extra ...dagui.SpanSnapshot) []dagui.SpanSnapshot {
		return append([]dagui.SpanSnapshot{
			{
				ID:        rootID,
				TraceID:   prettyTestTraceID(),
				Name:      "check",
				StartTime: start,
				EndTime:   start.Add(20 * time.Second),
				Final:     true,
			},
			{
				ID:        checkID,
				TraceID:   prettyTestTraceID(),
				Name:      "dag://?check=TestBar",
				CheckName: "dag://?check=TestBar",
				StartTime: start.Add(time.Second),
				EndTime:   start.Add(18 * time.Second),
				ParentID:  rootID,
				Status:    sdktrace.Status{Code: codes.Error, Description: ownError},
				Final:     true,
			},
		}, extra...)
	}
	otherTest := dagui.SpanSnapshot{
		ID:           testID,
		TraceID:      prettyTestTraceID(),
		Name:         "TestFoo",
		TestCaseName: "TestFoo",
		TestStatus:   dagui.TestStatusFailure,
		StartTime:    start.Add(2 * time.Second),
		EndTime:      start.Add(3 * time.Second),
		ParentID:     checkID,
		Status:       sdktrace.Status{Code: codes.Error, Description: "test failed"},
		Final:        true,
	}
	linkedOrigin := dagui.SpanSnapshot{
		ID:        originID,
		TraceID:   prettyTestTraceID(),
		Name:      "exec go test",
		StartTime: start.Add(2 * time.Second),
		EndTime:   start.Add(3 * time.Second),
		ParentID:  rootID,
		Status:    sdktrace.Status{Code: codes.Error, Description: "exit code: 1"},
		Final:     true,
	}
	for _, frontend := range []struct {
		name       string
		reportOnly bool
	}{
		{name: "report", reportOnly: true},
		{name: "terminal", reportOnly: false},
	} {
		for _, tc := range []struct {
			name      string
			spans     []dagui.SpanSnapshot
			origin    bool
			wantTests bool
		}{
			{name: "no tests beneath it", spans: spans()},
			{name: "another test's failure beneath it", spans: spans(otherTest), wantTests: true},
			{name: "another test's failure beneath it and a linked origin", spans: spans(otherTest, linkedOrigin), origin: true, wantTests: true},
		} {
			t.Run(frontend.name+"/"+tc.name, func(t *testing.T) {
				t.Setenv("NO_COLOR", "1")
				db := dagui.NewDB()
				db.ImportSnapshots(tc.spans)
				if tc.origin {
					db.Spans.Map[checkID].AddErrorOrigin(db.Spans.Map[originID])
				}
				db.SetPrimarySpan(rootID)
				fe := NewWithDB(io.Discard, db)
				fe.reportOnly = frontend.reportOnly
				fe.recalculateViewLocked()

				var buf bytes.Buffer
				if err := fe.FinalRender(&buf); err != nil {
					t.Fatalf("FinalRender: %v", err)
				}
				got := buf.String()
				if !strings.Contains(got, ownError) {
					t.Fatalf("report is missing the check's own error:\n%s", got)
				}
				if tc.wantTests {
					if !strings.Contains(got, "TestFoo FAIL") {
						t.Fatalf("report is missing the failing test beneath the check:\n%s", got)
					}
					if strings.Index(got, ownError) > strings.Index(got, "TESTS") {
						t.Fatalf("the check's own error should come before its tests:\n%s", got)
					}
				}
			})
		}
	}
}
