package dagui

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A lazy result evaluated twice, first partially and then completely, reads
// the same whether each evaluation links the call that returned the result,
// or only the first does and the later one links the first: the call's
// pending state, failure, activity, waiting time and rows, and the activity
// of the calls that started each evaluation, are identical, however the
// spans are delivered.
func TestResumeChainReadsLikeDirectResumes(t *testing.T) {
	t.Parallel()
	for _, names := range []resumeChainNames{
		{anchor: "resume withExec (metadata)", later: "resume withExec (execOutputs)"},
		{anchor: "resume withExec", later: "resume withExec"},
		{anchor: "open stored part (metadata)", later: "resume withExec (execOutputs)"},
	} {
		for _, outcome := range []string{"ok", "failed", "running"} {
			for _, laterFirst := range []bool{false, true} {
				name := fmt.Sprintf("%s then %s, %s", names.anchor, names.later, outcome)
				if laterFirst {
					name += ", later delivered first"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					direct := readResumeChain(t, names, outcome, laterFirst, false)
					chained := readResumeChain(t, names, outcome, laterFirst, true)
					require.Equal(t, direct, chained)

					// What the direct shape has always shown: the later
					// evaluation's outcome is the call's, its wait is the
					// call's, and the call's work shows beneath it.
					require.False(t, direct.call.pending)
					require.Equal(t, outcome == "running", direct.call.running)
					require.Equal(t, outcome == "failed", direct.call.failed)
					require.Equal(t, time.Second, direct.call.breakdown.Waiting)
					require.Contains(t, direct.rows, "2 exec echo hi")
					// The first consumer did its own work and nothing else.
					require.False(t, direct.firstConsumer.running)
					require.Equal(t, time.Second, direct.firstConsumer.active)
				})
			}
		}
	}
}

// resumeChainNames names a lazy result's two evaluations.
type resumeChainNames struct {
	anchor, later string
}

type resumeChainReading struct {
	call, firstConsumer, laterConsumer resumeChainSpanReading
	rows                               []string
}

type resumeChainSpanReading struct {
	pending       bool
	pendingReason []string
	running       bool
	failed        bool
	active        time.Duration
	breakdown     *TimeBreakdown
}

// readResumeChain records a call that returned a lazy result, the result's
// two evaluations, each started by its own later call (a consumer), and an
// outside span the later evaluation waited on. The first evaluation is
// partial (only its metadata); the later one completes, fails or still runs,
// and its exec is parented to the call, as the engine records it. Each
// evaluation links the call; or, chained, the later one links the first one
// instead. The spans arrive in one batch, or in two with the first
// evaluation in the second.
func readResumeChain(t *testing.T, names resumeChainNames, outcome string, laterFirst, chained bool) resumeChainReading {
	t.Helper()
	base := time.Unix(1000, 0)
	at := func(seconds float64) time.Time { return base.Add(time.Duration(seconds * float64(time.Second))) }
	laterEnd := at(5)
	if outcome == "running" {
		laterEnd = time.Time{}
	}
	id := func(n byte) SpanID { return SpanID{trace.SpanID{n}} }
	snapshot := func(n byte, parent SpanID, name string, start, end time.Time, edit func(*SpanSnapshot)) SpanSnapshot {
		s := SpanSnapshot{
			ID:        id(n),
			ParentID:  parent,
			Name:      name,
			StartTime: start,
			EndTime:   end,
			Status:    sdktrace.Status{Code: codes.Ok},
		}
		if end.IsZero() {
			s.Status = sdktrace.Status{}
		}
		if edit != nil {
			edit(&s)
		}
		return s
	}
	root, call, firstConsumer, laterConsumer := id(1), id(2), id(3), id(4)
	outside, anchor, later := id(5), id(6), id(7)
	failed := func(s *SpanSnapshot) {
		if outcome == "failed" {
			s.Status = sdktrace.Status{Code: codes.Error}
		}
	}
	cause := call
	if chained {
		cause = anchor
	}
	first := []SpanSnapshot{
		snapshot(1, SpanID{}, "dagger call", at(0), laterEnd, nil),
		snapshot(2, root, "Container.withExec", at(0), at(1), func(s *SpanSnapshot) {
			s.Pending = true
			s.Status = sdktrace.Status{}
		}),
		snapshot(3, root, "Container.stdout", at(1), at(2), nil),
		snapshot(4, root, "Container.export", at(2), laterEnd, failed),
		snapshot(5, root, "outside work", at(2), at(4), nil),
		snapshot(7, laterConsumer, names.later, at(2), laterEnd, func(s *SpanSnapshot) {
			s.Passthrough = true
			s.Links = []SpanLink{
				{SpanContext: SpanContext{SpanID: cause}},
				{
					SpanContext: SpanContext{SpanID: outside},
					Purpose:     telemetryattrs.LinkPurposeWait,
					WaitReason:  "test",
					WaitStart:   at(2.5),
					WaitEnd:     at(3.5),
				},
			}
			failed(s)
		}),
		snapshot(8, call, "exec echo hi", at(3.5), laterEnd, failed),
	}
	second := []SpanSnapshot{
		snapshot(6, firstConsumer, names.anchor, at(1), at(2), func(s *SpanSnapshot) {
			s.Passthrough = true
			s.Partial = true
			s.Links = []SpanLink{{SpanContext: SpanContext{SpanID: call}}}
		}),
	}

	db := NewDB()
	if laterFirst {
		db.ImportSnapshots(first)
		db.ImportSnapshots(second)
	} else {
		db.ImportSnapshots(append(second, first...))
	}
	require.True(t, db.Spans.Map[anchor].Received)
	require.True(t, db.Spans.Map[later].Received)

	now := at(10)
	read := func(id SpanID) resumeChainSpanReading {
		span := db.Spans.Map[id]
		pending, reason := span.PendingReason()
		require.Equal(t, span.IsPending(), pending, "IsPending and PendingReason agree")
		return resumeChainSpanReading{
			pending:       pending,
			pendingReason: reason,
			running:       span.IsRunningOrEffectsRunning(),
			failed:        span.IsFailedOrCausedFailure(),
			active:        span.Activity.Duration(now),
			breakdown:     span.TimeBreakdown(now),
		}
	}
	reading := resumeChainReading{
		call:          read(call),
		firstConsumer: read(firstConsumer),
		laterConsumer: read(laterConsumer),
	}
	opts := FrontendOpts{Verbosity: ExpandCompletedVerbosity}
	for _, row := range db.RowsView(opts).Rows(opts).Order {
		reading.rows = append(reading.rows, fmt.Sprintf("%d %s", row.Depth, row.Span.Name))
	}
	return reading
}
