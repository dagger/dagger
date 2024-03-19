package idtui

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/tracing"
)

type DB struct {
	Traces   map[trace.TraceID]*Trace
	Spans    map[trace.SpanID]*Span
	Tasks    map[trace.SpanID][]*Task
	Children map[trace.SpanID]map[trace.SpanID]struct{}

	Logs        map[trace.SpanID]*Vterm
	LogWidth    int
	PrimarySpan trace.SpanID
	PrimaryLogs map[trace.SpanID][]*sdklog.LogData

	Calls     map[string]*callpbv1.Call
	Outputs   map[string]map[string]struct{}
	OutputOf  map[string]map[string]struct{}
	Intervals map[string]map[time.Time]*Span
}

func NewDB() *DB {
	return &DB{
		Traces:   make(map[trace.TraceID]*Trace),
		Spans:    make(map[trace.SpanID]*Span),
		Tasks:    make(map[trace.SpanID][]*Task),
		Children: make(map[trace.SpanID]map[trace.SpanID]struct{}),

		Logs:        make(map[trace.SpanID]*Vterm),
		LogWidth:    -1,
		PrimaryLogs: make(map[trace.SpanID][]*sdklog.LogData),

		Calls:     make(map[string]*callpbv1.Call),
		OutputOf:  make(map[string]map[string]struct{}),
		Outputs:   make(map[string]map[string]struct{}),
		Intervals: make(map[string]map[time.Time]*Span),
	}
}

func (db *DB) AllTraces() []*Trace {
	traces := make([]*Trace, 0, len(db.Traces))
	for _, traceData := range db.Traces {
		traces = append(traces, traceData)
	}
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].Epoch.After(traces[j].Epoch)
	})
	return traces
}

func (db *DB) SetWidth(width int) {
	db.LogWidth = width
	for _, vt := range db.Logs {
		vt.SetWidth(width)
	}
}

var _ sdktrace.SpanExporter = (*DB)(nil)

func (db *DB) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		traceID := span.SpanContext().TraceID()

		traceData, found := db.Traces[traceID]
		if !found {
			traceData = &Trace{
				ID:    traceID,
				Epoch: span.StartTime(),
				End:   span.EndTime(),
				db:    db,
			}
			db.Traces[traceID] = traceData
		}

		if span.StartTime().Before(traceData.Epoch) {
			slog.Debug("new epoch", "old", traceData.Epoch, "new", span.StartTime())
			traceData.Epoch = span.StartTime()
		}

		if span.EndTime().Before(span.StartTime()) {
			traceData.IsRunning = true
		}

		if span.EndTime().After(traceData.End) {
			slog.Debug("new end", "old", traceData.End, "new", span.EndTime())
			traceData.End = span.EndTime()
		}

		db.maybeRecordSpan(traceData, span)
		db.maybeRecordTask(span)
	}
	return nil
}

var _ sdklog.LogExporter = (*DB)(nil)

func (db *DB) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	for _, log := range logs {
		slog.Debug("exporting log", "span", log.SpanID, "body", log.Body().AsString())

		// render vterm for TUI
		_, _ = fmt.Fprint(db.spanLogs(log.SpanID), log.Body().AsString())

		if log.SpanID == db.PrimarySpan {
			// buffer raw logs so we can replay them later
			db.PrimaryLogs[log.SpanID] = append(db.PrimaryLogs[log.SpanID], log)
		}
	}
	return nil
}

func (db *DB) Shutdown(ctx context.Context) error {
	return nil // noop
}

func (db *DB) spanLogs(id trace.SpanID) *Vterm {
	term, found := db.Logs[id]
	if !found {
		term = NewVterm()
		if db.LogWidth > -1 {
			term.SetWidth(db.LogWidth)
		}
		db.Logs[id] = term
	}
	return term
}

func (db *DB) SetPrimarySpan(span trace.SpanID) {
	db.PrimarySpan = span
}

func (db *DB) maybeRecordSpan(traceData *Trace, span sdktrace.ReadOnlySpan) {
	spanID := span.SpanContext().SpanID()

	spanData := &Span{
		ReadOnlySpan: span,
		Primary:      spanID == db.PrimarySpan,
		db:           db,
		trace:        traceData,
	}

	slog.Debug("recording span", "span", span.Name(), "id", spanID)

	db.Spans[spanID] = spanData

	// track parent/child relationships
	if parent := span.Parent(); parent.IsValid() {
		if db.Children[parent.SpanID()] == nil {
			db.Children[parent.SpanID()] = make(map[trace.SpanID]struct{})
		}
		slog.Debug("recording span child", "span", span.Name(), "parent", parent.SpanID(), "child", spanID)
		db.Children[parent.SpanID()][spanID] = struct{}{}
	}

	attrs := span.Attributes()

	var digest string
	if digestAttr, ok := getAttr(attrs, tracing.DagDigestAttr); ok {
		digest = digestAttr.AsString()
		spanData.Digest = digest

		// keep track of intervals seen for a digest
		if db.Intervals[digest] == nil {
			db.Intervals[digest] = make(map[time.Time]*Span)
		}

		db.Intervals[digest][span.StartTime()] = spanData
	}

	for _, attr := range attrs {
		switch attr.Key {
		case tracing.DagCallAttr:
			var call callpbv1.Call
			if err := call.Decode(attr.Value.AsString()); err != nil {
				slog.Warn("failed to decode id", "err", err)
				continue
			}

			spanData.Call = &call

			// Seeing loadFooFromID is only really interesting if it actually
			// resulted in evaluating the ID, so we set Passthrough, which will only
			// show its children.
			if call.Field == fmt.Sprintf("load%sFromID", call.Type.ToAST().Name()) {
				spanData.Passthrough = true
			}

			// We also don't care about seeing the id field selection itself, since
			// it's more noisy and confusing than helpful. We'll still show all the
			// spans leadning up to it, just not the ID selection.
			if call.Field == "id" {
				spanData.Ignore = true
			}

			if digest != "" {
				db.Calls[digest] = &call
			}

		case tracing.LLBOpAttr:
			// TODO

		case tracing.CachedAttr:
			spanData.Cached = attr.Value.AsBool()

		case tracing.CanceledAttr:
			spanData.Canceled = attr.Value.AsBool()

		case tracing.UIEncapsulateAttr:
			spanData.Encapsulate = attr.Value.AsBool()

		case tracing.InternalAttr:
			spanData.Internal = attr.Value.AsBool()

		case tracing.DagInputsAttr:
			spanData.Inputs = attr.Value.AsStringSlice()

		case tracing.DagOutputAttr:
			output := attr.Value.AsString()
			if digest == "" {
				slog.Warn("output attribute is set, but a digest is not?")
			} else {
				slog.Debug("recording output", "digest", digest, "output", output)

				// parent -> child
				if db.Outputs[digest] == nil {
					db.Outputs[digest] = make(map[string]struct{})
				}
				db.Outputs[digest][output] = struct{}{}

				// child -> parent
				if db.OutputOf[output] == nil {
					db.OutputOf[output] = make(map[string]struct{})
				}
				db.OutputOf[output][digest] = struct{}{}
			}

		case "rpc.service":
			spanData.Passthrough = true
		}
	}
}

func (db *DB) PrimarySpanForTrace(traceID trace.TraceID) *Span {
	for _, span := range db.Spans {
		spanCtx := span.SpanContext()
		if span.Primary && spanCtx.TraceID() == traceID {
			return span
		}
	}
	return nil
}

func (db *DB) maybeRecordTask(span sdktrace.ReadOnlySpan) {
	attrs := span.Attributes()

	if _, isTask := getAttr(attrs, tracing.TaskParentAttr); !isTask {
		return
	}

	parent := span.Parent().SpanID()

	tasks := db.Tasks[parent]

	task := &Task{
		Span:      span,
		Name:      span.Name(),
		Started:   span.StartTime(),
		Completed: span.EndTime(),
	}

	if attr, ok := getAttr(attrs, tracing.ProgressCurrentAttr); ok {
		task.Current = attr.AsInt64()
	}
	if attr, ok := getAttr(attrs, tracing.ProgressTotalAttr); ok {
		task.Total = attr.AsInt64()
	}

	taskID := span.SpanContext().SpanID()

	var updated bool
	for i, task := range tasks {
		if task.Span.SpanContext().SpanID() == taskID {
			tasks[i] = task
		}
	}
	if !updated {
		tasks = append(tasks, task)
		db.Tasks[parent] = tasks
	}
}

func (db *DB) HighLevelSpan(call *callpbv1.Call) *Span {
	return db.MostInterestingSpan(db.Simplify(call).Digest)
}

func (db *DB) MostInterestingSpan(dig string) *Span {
	var earliest *Span
	var earliestCached bool
	vs := make([]sdktrace.ReadOnlySpan, 0, len(db.Intervals[dig]))
	for _, span := range db.Intervals[dig] {
		vs = append(vs, span)
	}
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].StartTime().Before(vs[j].StartTime())
	})
	for _, span := range db.Intervals[dig] {
		// a running vertex is always most interesting, and these are already in
		// order
		if span.IsRunning() {
			return span
		}
		switch {
		case earliest == nil:
			// always show _something_
			earliest = span
			earliestCached = span.Cached
		case span.Cached:
			// don't allow a cached vertex to override a non-cached one
		case earliestCached:
			// unclear how this would happen, but non-cached versions are always more
			// interesting
			earliest = span
		case span.StartTime().Before(earliest.StartTime()):
			// prefer the earliest active interval
			earliest = span
		}
	}
	return earliest
}

// func (db *DB) IsTransitiveDependency(dig, depDig string) bool {
// 	for _, v := range db.Intervals[dig] {
// 		for _, dig := range v.Inputs {
// 			if dig == depDig {
// 				return true
// 			}
// 			if db.IsTransitiveDependency(dig, depDig) {
// 				return true
// 			}
// 		}
// 		// assume they all have the same inputs
// 		return false
// 	}
// 	return false
// }

func (*DB) Close() error {
	return nil
}

func (db *DB) MustCall(dig string) *callpbv1.Call {
	call, ok := db.Calls[dig]
	if !ok {
		// Sometimes may see a call's digest before the call itself.
		//
		// The loadFooFromID APIs for example will emit their call via their span
		// before loading the ID, and its ID argument will just be a digest like
		// anything else.
		return &callpbv1.Call{
			Field: "unknown",
			Type: &callpbv1.Type{
				NamedType: "Void",
			},
			Digest: dig,
		}
	}
	return call
}

func (db *DB) litSize(lit *callpbv1.Literal) int {
	switch x := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return db.idSize(db.MustCall(x.CallDigest))
	case *callpbv1.Literal_List:
		size := 0
		for _, lit := range x.List.GetValues() {
			size += db.litSize(lit)
		}
		return size
	case *callpbv1.Literal_Object:
		size := 0
		for _, lit := range x.Object.GetValues() {
			size += db.litSize(lit.GetValue())
		}
		return size
	}
	return 1
}

func (db *DB) idSize(id *callpbv1.Call) int {
	size := 0
	for id := id; id != nil; id = db.Calls[id.ReceiverDigest] {
		size++
		size += len(id.Args)
		for _, arg := range id.Args {
			size += db.litSize(arg.GetValue())
		}
	}
	return size
}

func (db *DB) Simplify(call *callpbv1.Call) (smallest *callpbv1.Call) {
	smallest = call
	creators, ok := db.OutputOf[call.Digest]
	if !ok {
		return
	}
	var smallestSize = db.idSize(smallest)
	var simplified bool
	for creatorDig := range creators {
		creator, ok := db.Calls[creatorDig]
		if ok {
			if size := db.idSize(creator); size < smallestSize {
				smallest = creator
				smallestSize = size
				simplified = true
			}
		}
	}
	if simplified {
		return db.Simplify(smallest)
	}
	return
}

func getAttr(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}
