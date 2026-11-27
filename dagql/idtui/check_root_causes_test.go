package idtui

import (
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// A passthrough span is never a check's root cause. A check that failed on
// its own span, with its function's run span and the span its SDK ran the
// function under failing beneath it, is its own root cause, not a
// "view:View.__check_failing" row. A failed span beneath that isn't
// passthrough, and an origin the check links, are still root causes.
func TestCheckRootCausesSkipPassthrough(t *testing.T) {
	traceID := prettyTestTraceID()
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	failed := sdktrace.Status{Code: codes.Error, Description: "check body failed"}
	ok := sdktrace.Status{Code: codes.Ok}
	span := func(id, parent byte, name string, status sdktrace.Status, passthrough bool) dagui.SpanSnapshot {
		snap := dagui.SpanSnapshot{
			ID: prettyTestSpanID(id), TraceID: traceID, Name: name,
			StartTime: start, EndTime: end, Final: true, Status: status, Passthrough: passthrough,
		}
		if parent != 0 {
			snap.ParentID = prettyTestSpanID(parent)
		}
		return snap
	}
	check := func(id byte, name string) dagui.SpanSnapshot {
		snap := span(id, 0, "dag://?check="+name, failed, false)
		snap.CheckName = "dag://?check=" + name
		return snap
	}
	names := func(spans []*dagui.Span) []string {
		var out []string
		for _, s := range spans {
			out = append(out, s.Name)
		}
		return out
	}

	t.Run("passthrough leaf", func(t *testing.T) {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			check(1, "failing"),
			span(2, 1, "Check.__run", ok, true),
			span(3, 2, "View.__check_failing", failed, true),
			span(4, 3, "view:View.__check_failing", failed, true),
		})
		root := db.Spans.Map[prettyTestSpanID(1)]
		require.Equal(t, []string{"dag://?check=failing"}, names(CheckRootCauses(root)))
	})

	t.Run("failed span under passthrough", func(t *testing.T) {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			check(1, "failing"),
			span(2, 1, "Check.__run", ok, true),
			span(3, 2, "View.__check_failing", failed, true),
			span(4, 3, "load source", failed, false),
			span(5, 4, "view:helper", failed, true),
		})
		root := db.Spans.Map[prettyTestSpanID(1)]
		require.Equal(t, []string{"dag://?check=failing", "load source"}, names(CheckRootCauses(root)))
	})

	t.Run("cause-linked passthrough leaf", func(t *testing.T) {
		// A failed passthrough span parented under another check, and
		// cause-linked to a span beneath this one, is this check's child too.
		// Its failure shows on this check, not on the other check's spans.
		db := dagui.NewDB()
		resume := span(9, 8, "resume withExec", failed, true)
		resume.Links = []dagui.SpanLink{{
			SpanContext: dagui.SpanContext{TraceID: traceID, SpanID: prettyTestSpanID(4)},
		}}
		db.ImportSnapshots([]dagui.SpanSnapshot{
			check(1, "one"),
			span(2, 1, "Check.__run", ok, true),
			span(3, 2, "View.__check_one", failed, true),
			span(4, 3, "Container.withExec", ok, false),
			check(7, "two"),
			span(8, 7, "Container.sync", failed, false),
			resume,
		})
		root := db.Spans.Map[prettyTestSpanID(1)]
		require.Equal(t, []string{"dag://?check=one"}, names(CheckRootCauses(root)))
	})

	t.Run("failed exec in function plumbing", func(t *testing.T) {
		// A module entrypoint's dispatcher exec fails because the
		// function did: it only repeats the check's own failure.
		db := dagui.NewDB()
		plumbing := span(4, 3, "call module entrypoint", ok, false)
		plumbing.FunctionPlumbing = true
		db.ImportSnapshots([]dagui.SpanSnapshot{
			check(1, "failing"),
			span(2, 1, "Check.__run", ok, true),
			span(3, 2, "View.__check_failing", failed, true),
			plumbing,
			span(5, 4, "withExec python -m dagger.mod call", failed, false),
		})
		root := db.Spans.Map[prettyTestSpanID(1)]
		require.Equal(t, []string{"dag://?check=failing"}, names(CheckRootCauses(root)))
	})

	t.Run("deeper origin", func(t *testing.T) {
		db := dagui.NewDB()
		deep := check(1, "deep")
		deep.Links = []dagui.SpanLink{{
			SpanContext: dagui.SpanContext{TraceID: traceID, SpanID: prettyTestSpanID(5)},
			Purpose:     telemetry.LinkPurposeErrorOrigin,
		}}
		db.ImportSnapshots([]dagui.SpanSnapshot{
			deep,
			span(2, 1, "Check.__run", ok, true),
			span(3, 2, "View.__check_deep", failed, true),
			span(4, 3, "view:View.__check_deep", failed, true),
			span(5, 4, "Container.withExec", failed, false),
		})
		root := db.Spans.Map[prettyTestSpanID(1)]
		require.Equal(t, []string{"Container.withExec"}, names(CheckRootCauses(root)))
	})
}
