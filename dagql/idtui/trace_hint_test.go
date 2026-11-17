package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestTraceHintsUseTraceCommand: the report's drill-in suggestions (MORE
// DETAILS) and "full:" log hints name the command the CLI says can read the
// trace -- 'dagger trace' for a trace the engine's store served, since the
// 'dagger cloud' commands read Cloud only -- and default to 'dagger cloud
// traces view' when none was given. A live run (no trace) suggests nothing.
func TestTraceHintsUseTraceCommand(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	traceID := prettyTestTraceID()
	rootID, checkID, testID := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)
	failed := sdktrace.Status{Code: codes.Error, Description: "failed"}

	newFrontend := func() *frontendPretty {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			{ID: rootID, TraceID: traceID, Name: "root", StartTime: start, EndTime: end, Final: true, Status: failed},
			{
				ID: checkID, TraceID: traceID, ParentID: rootID, Name: "check unit",
				StartTime: start, EndTime: end, CheckName: "unit", Final: true, Status: failed,
			},
			{
				ID: testID, TraceID: traceID, ParentID: checkID, Name: "unit failure",
				StartTime: start, EndTime: end, TestCaseName: "unit failure",
				TestStatus: dagui.TestStatusFailure, Final: true, Status: failed,
			},
		})
		db.SetPrimarySpan(rootID)
		fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
		fe.SetPrimary(rootID)
		return fe
	}

	for _, tc := range []struct {
		name    string
		command string
		want    string
	}{
		{name: "engine store", command: "dagger trace", want: "dagger trace " + traceID.String()},
		{name: "cloud", command: "dagger cloud traces view", want: "dagger cloud traces view " + traceID.String()},
		{name: "unspecified", command: "", want: "dagger cloud traces view " + traceID.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := newFrontend()
			fe.traceID, fe.traceCommand = traceID.String(), tc.command
			require.Equal(t, tc.want, fe.traceViewCommand())

			details := strings.Join(fe.renderSuggestionsSection(nil), "\n")
			require.Contains(t, details, "MORE DETAILS")
			require.Contains(t, details, tc.want+` --check "unit"`)
			require.Contains(t, details, tc.want+` --test "unit failure"`)
			if tc.want != "dagger cloud traces view "+traceID.String() {
				require.NotContains(t, details, "dagger cloud traces view")
			}

			out := NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
			test := fe.db.Spans.Map[testID]
			lines := errorWindowLines(out, []string{"boom"}, "", fe.traceViewCommand(), traceLogsHintTarget(test))
			require.Equal(t, []string{"boom", "full: " + tc.want + ` --log --test "unit failure"`}, lines)
		})
	}

	t.Run("live run", func(t *testing.T) {
		fe := newFrontend()
		require.Empty(t, fe.traceViewCommand())
		require.Nil(t, fe.renderSuggestionsSection(nil))
		out := NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
		require.Equal(t, []string{"boom"}, errorWindowLines(out, []string{"boom"}, "", fe.traceViewCommand(), traceLogsHintTarget(fe.db.Spans.Map[testID])))
	})

	t.Run("SetTraceID", func(t *testing.T) {
		fe := newFrontend()
		fe.SetTraceID(traceID.String(), "dagger trace")
		fe.tui.Step()
		require.Equal(t, "dagger trace "+traceID.String(), fe.traceViewCommand())
	})
}
