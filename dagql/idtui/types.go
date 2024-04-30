package idtui

import (
	"sort"
	"time"

	"github.com/dagger/dagger/engine/slog"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Trace struct {
	ID         trace.TraceID
	Epoch, End time.Time
	IsRunning  bool
	db         *DB
}

func (trace *Trace) HexID() string {
	return trace.ID.String()
}

func (trace *Trace) Name() string {
	if span := trace.db.PrimarySpanForTrace(trace.ID); span != nil {
		return span.Name()
	}
	return "unknown"
}

func (trace *Trace) PrimarySpan() *Span {
	return trace.db.PrimarySpanForTrace(trace.ID)
}

type Task struct {
	Span      sdktrace.ReadOnlySpan
	Name      string
	Current   int64
	Total     int64
	Started   time.Time
	Completed time.Time
}

func CollectSpans(db *DB, traceID trace.TraceID) []*Span {
	var spans []*Span //nolint:prealloc
	for _, span := range db.Spans {
		if span.Ignore {
			continue
		}
		if traceID.IsValid() && span.SpanContext().TraceID() != traceID {
			continue
		}
		if span.Mask && span.Parent().IsValid() {
			masked := db.Spans[span.Parent().SpanID()]
			if masked != nil {
				masked.Passthrough = true
			} else {
				// FIXME(vito): still investigating why this happens, but in
				// the mean time, better not to panic
				slog.Warn("masked parent span not found; possible data loss?",
					"traceID", traceID,
					"spanID", span.SpanContext().SpanID(),
					"parentID", span.Parent().SpanID())
			}
		}
		spans = append(spans, span)
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].IsBefore(spans[j])
	})
	return spans
}

func CollectRows(steps []*Span) []*TraceRow {
	var rows []*TraceRow
	WalkSteps(steps, func(row *TraceRow) {
		if row.Parent != nil {
			row.Parent.Children = append(row.Parent.Children, row)
		} else {
			rows = append(rows, row)
		}
	})
	return rows
}

type TraceRow struct {
	Span *Span

	Parent *TraceRow

	IsRunning bool
	Chained   bool

	Children  []*TraceRow
	Collapsed bool
}

type Pipeline []*TraceRow

func CollectPipelines(rows []*TraceRow) []Pipeline {
	pls := []Pipeline{}
	var cur Pipeline
	for _, r := range rows {
		switch {
		case len(cur) == 0:
			cur = append(cur, r)
		case r.Chained:
			cur = append(cur, r)
		case len(cur) > 0:
			pls = append(pls, cur)
			cur = Pipeline{r}
		}
	}
	if len(cur) > 0 {
		pls = append(pls, cur)
	}
	return pls
}

type LogsView struct {
	Primary *Span
	Body    []*TraceRow
}

func CollectLogsView(rows []*TraceRow) *LogsView {
	view := &LogsView{}
	for _, row := range rows {
		if row.Span.Primary {
			// promote children of primary vertex to the top-level
			for _, child := range row.Children {
				child.Parent = nil
			}
			view.Primary = row.Span
			// reveal anything 'extra' below the primary content
			view.Body = append(row.Children, view.Body...)
		} else {
			// reveal anything 'extra' by default (fail open)
			view.Body = append(view.Body, row)
		}
	}
	return view
}

const (
	TooFastThreshold = 100 * time.Millisecond
	GCThreshold      = 1 * time.Second
)

func (row *TraceRow) Depth() int {
	if row.Parent == nil {
		return 0
	}
	return row.Parent.Depth() + 1
}

func (row *TraceRow) setRunning() {
	row.IsRunning = true
	if row.Parent != nil && !row.Parent.IsRunning {
		row.Parent.setRunning()
	}
}

func WalkSteps(spans []*Span, f func(*TraceRow)) {
	var lastRow *TraceRow
	seen := map[trace.SpanID]bool{}
	var walk func(*Span, *TraceRow)
	walk = func(span *Span, parent *TraceRow) {
		spanID := span.SpanContext().SpanID()
		if seen[spanID] {
			return
		}
		if span.Passthrough {
			for _, child := range span.Children() {
				walk(child, parent)
			}
			return
		}
		row := &TraceRow{
			Span:   span,
			Parent: parent,
		}
		if base, ok := span.Base(); ok && lastRow != nil {
			row.Chained = base.Digest == lastRow.Span.Digest
		}
		if span.IsRunning() {
			row.setRunning()
		}
		f(row)
		lastRow = row
		seen[spanID] = true
		for _, child := range span.Children() {
			walk(child, row)
		}
		lastRow = row
	}
	for _, step := range spans {
		walk(step, nil)
	}
}
