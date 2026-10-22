package idtui

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestSearchFindsCollapsedMatches checks that search finds matches beneath
// collapsed rows, whose trees the view doesn't build, in tree order, and
// navigates to one whose home is beneath a cause rather than its parent.
func TestSearchFindsCollapsedMatches(t *testing.T) {
	start := time.Unix(100, 0)
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	id := prettyTestSpanID
	span := func(n byte, parent byte, name string) dagui.SpanSnapshot {
		snap := dagui.SpanSnapshot{
			ID: id(n), TraceID: prettyTestTraceID(), Name: name,
			StartTime: at(int(n)), EndTime: at(int(n) + 1),
		}
		if parent != 0 {
			snap.ParentID = id(parent)
		}
		return snap
	}
	hosted := span(6, 2, "needle three")
	hosted.Links = []dagui.SpanLink{{
		SpanContext: dagui.SpanContext{SpanID: id(5)},
		Purpose:     "cause",
	}}
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		span(1, 0, "root"),
		span(2, 1, "a"),
		span(3, 2, "needle one"),
		span(4, 1, "needle two"),
		span(5, 1, "c"),
		hosted,
	})
	db.SetPrimarySpan(id(1))
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	fe.FrontendOpts.ZoomedSpan = id(1)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.setupTUI()
	fe.recalculateViewLocked()
	require.Nil(t, fe.rows.BySpan[id(3)], "a should be collapsed")
	require.Nil(t, fe.rows.BySpan[id(6)], "c should be collapsed")

	fe.confirmSearch("needle")
	var got []dagui.SpanID
	for _, m := range fe.searchMatches {
		got = append(got, m.spanID)
	}
	require.Equal(t, []dagui.SpanID{id(3), id(4), id(6)}, got)

	// The third match's home is beneath its cause, c.
	fe.searchIdx = 2
	fe.goToSearchMatch(2)
	require.Equal(t, id(6), fe.FocusedSpan)
	row := fe.rows.BySpan[id(6)]
	require.NotNil(t, row)
	require.Equal(t, id(5), row.Parent.Span.ID)
}

// TestSearchFindsUnloadedLogs: with a source that can search (an engine
// store), a search finds lines in logs never loaded; going to the match reads
// them and lands on the line. Without one (Cloud), the hint says the search
// covers only loaded logs.
func TestSearchFindsUnloadedLogs(t *testing.T) {
	start := time.Unix(100, 0)
	traceID := prettyTestTraceID()
	rootID, quiet, loud := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	src := &staticSource{
		id: traceID.String(),
		priority: []dagui.SpanSnapshot{
			{ID: rootID, TraceID: traceID, Name: "root", StartTime: start, EndTime: start.Add(time.Hour), Final: true},
			{ID: quiet, TraceID: traceID, ParentID: rootID, Name: "quiet", StartTime: start, EndTime: start.Add(time.Hour), HasLogs: true, Final: true},
			{ID: loud, TraceID: traceID, ParentID: rootID, Name: "loud", StartTime: start.Add(time.Second), EndTime: start.Add(time.Hour), HasLogs: true, Final: true},
		},
		own: map[string][]traceLog{
			quiet.String(): {{SpanID: quiet.String(), Body: "nothing here\n"}},
			loud.String():  {{SpanID: loud.String(), Body: "first\n"}, {SpanID: loud.String(), Body: "the needle line\n"}},
		},
	}
	sess := newTraceSession(t, src, nil)
	sess.fe.dispatch(func() { sess.fe.confirmSearch("needle") })
	sess.Render()
	require.Contains(t, sess.fe.searchCountHint("next"), "loaded logs only")
	require.False(t, sess.Network().fetchedLog(loud))

	sess.fe.SetLogSearch(dagui.TraceID{}, func(_ context.Context, pattern string) (LogSearchHits, error) {
		if pattern != "needle" {
			return LogSearchHits{}, nil
		}
		// A hit in a span not loaded is counted, and a partial search said.
		return LogSearchHits{Targets: []LogTarget{{Trace: traceID, Span: loud}, {Trace: traceID, Span: prettyTestSpanID(99)}}, Partial: true}, nil
	})
	sess.Render()
	sess.fe.dispatch(func() { sess.fe.confirmSearch("needle") })
	require.Eventually(t, func() bool {
		sess.Render()
		return len(sess.fe.searchMatches) > 0
	}, 5*time.Second, 10*time.Millisecond)
	hint := sess.fe.searchCountHint("next")
	require.NotContains(t, hint, "loaded logs only")
	require.Contains(t, hint, "more not searched")
	require.Contains(t, hint, "1 in spans not loaded")

	// The first match is selected as it arrives: the span expands and its
	// logs are read, and the match becomes the line itself.
	out := sess.Render()
	require.True(t, sess.Network().fetchedLog(loud), "going to the match reads its logs")
	require.Contains(t, out, "the needle line")
	require.Len(t, sess.fe.searchMatches, 1)
	m := sess.fe.searchMatches[sess.fe.searchIdx]
	require.Equal(t, loud, m.spanID)
	require.GreaterOrEqual(t, m.logRow, 0, "the remote match gave way to the line")
}
