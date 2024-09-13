package dagui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type DB struct {
	PrimarySpan SpanID
	PrimaryLogs map[SpanID][]sdklog.Record

	Epoch, End time.Time

	Spans    *OrderedSet[SpanID, *Span]
	RootSpan *Span

	Calls     map[string]*callpbv1.Call
	Outputs   map[string]map[string]struct{}
	OutputOf  map[string]map[string]struct{}
	Intervals map[string]map[time.Time]*Span

	CauseSpans  map[string]SpanSet
	EffectSpans map[string]SpanSet

	CompletedEffects map[string]bool
	FailedEffects    map[string]bool

	// updatedSpans is a set of spans that have been updated since the last
	// sync, which includes any parent spans whose overall active time intervals
	// or status were modified via a child or linked span.
	updatedSpans SpanSet
}

func NewDB() *DB {
	return &DB{
		PrimaryLogs: make(map[SpanID][]sdklog.Record),

		Spans: NewSpanSet(),

		Calls:     make(map[string]*callpbv1.Call),
		OutputOf:  make(map[string]map[string]struct{}),
		Outputs:   make(map[string]map[string]struct{}),
		Intervals: make(map[string]map[time.Time]*Span),

		CompletedEffects: make(map[string]bool),
		FailedEffects:    make(map[string]bool),
		CauseSpans:       make(map[string]SpanSet),
		EffectSpans:      make(map[string]SpanSet),

		updatedSpans: NewSpanSet(),
	}
}

func (db *DB) UpdatedSnapshots(filter map[SpanID]bool) []SpanSnapshot {
	snapshots := snapshotSpans(db.updatedSpans.Order, func(span *Span) bool {
		return filter == nil || filter[span.ParentID]
	})
	db.updatedSpans = NewSpanSet()
	return snapshots
}

func (db *DB) ImportSnapshots(snapshots []SpanSnapshot) {
	for _, snapshot := range snapshots {
		span := db.findOrAllocSpan(snapshot.ID)
		span.Received = true
		span.SpanSnapshot = snapshot
		db.integrateSpan(span)
	}
}

// Matches returns true if the span matches the filter, looking through
// Passthrough span parents until a match is found or a non-Passthrough span
// is reached.
func (span *Span) Matches(match func(*Span) bool) bool {
	if match(span) {
		return true
	}
	if span.ParentSpan != nil && span.ParentSpan.Passthrough {
		return span.ParentSpan.Matches(match)
	}
	return false
}

func snapshotSpans(spans []*Span, filter func(*Span) bool) []SpanSnapshot {
	var filtered []SpanSnapshot
	for _, span := range spans {
		if span.Matches(filter) {
			filtered = append(filtered, span.Snapshot())
		}
	}
	return filtered
}

func (db *DB) SpanSnapshots(id SpanID) []SpanSnapshot {
	return snapshotSpans(db.Spans.Order, func(span *Span) bool {
		return span.ParentID == id || span.ID == id
	})
}

var _ sdktrace.SpanExporter = (*DB)(nil)

func (db *DB) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		db.recordOTelSpan(span)
	}
	return nil
}

func (db *DB) LogExporter() sdklog.Exporter {
	return DBLogExporter{db}
}

type DBLogExporter struct {
	*DB
}

func (db DBLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		if log.Body().AsString() == "" {
			// eof; ignore
			continue
		}
		spanID := SpanID{log.SpanID()}
		if spanID == db.PrimarySpan {
			// buffer raw logs so we can replay them later
			db.PrimaryLogs[spanID] = append(db.PrimaryLogs[spanID], log)
		}
	}
	return nil
}

func (db *DB) Shutdown(ctx context.Context) error {
	return nil // noop
}

func (db *DB) ForceFlush(ctx context.Context) error {
	return nil // noop
}

// SetPrimarySpan allows the primary span to be explicitly set to a particular
// span. normally we assume the root span is the primary span, but in a nested
// scenario we never actually see the root span, so the CLI explicitly sets it
// to the span it created.
func (db *DB) SetPrimarySpan(span SpanID) {
	db.PrimarySpan = span
}

func (db *DB) initSpan(spanID SpanID) *Span {
	spanData, found := db.Spans.Map[spanID]
	if !found {
		spanData = db.newSpan(spanID)
		db.Spans.Add(spanData)
	}
	return spanData
}

func (db *DB) findOrAllocSpan(spanID SpanID) *Span {
	spanData, found := db.Spans.Map[spanID]
	if found {
		return spanData
	}
	return db.newSpan(spanID)
}

func (db *DB) newSpan(spanID SpanID) *Span {
	// TODO: this fools things into thinking they're a root span...?
	return &Span{
		SpanSnapshot: SpanSnapshot{
			ID: spanID,
		},
		ChildSpans:   NewSpanSet(),
		LinkedFrom:   NewSpanSet(),
		LinksTo:      NewSpanSet(),
		RunningSpans: NewSpanSet(),
		FailedLinks:  NewSpanSet(),
		db:           db,
	}
}

func (db *DB) recordOTelSpan(span sdktrace.ReadOnlySpan) { //nolint: gocyclo
	spanID := SpanID{span.SpanContext().SpanID()}

	// mark the span as updated so we sync it to the frontend,
	// if this database is running on a backend
	//
	// NOTE: any updates to *other* spans should also be marked! for example, if
	// we update the status or active time of any linked/parent spans, those
	// should go in here too.

	// create or update the span itself
	spanData := db.findOrAllocSpan(spanID)
	spanData.Received = true
	spanData.ParentID.SpanID = span.Parent().SpanID()
	spanData.Name = span.Name()
	spanData.StartTime = span.StartTime()
	spanData.EndTime = span.EndTime()
	spanData.Status = span.Status()
	spanData.Links = make([]SpanContext, len(span.Links()))
	for i, link := range span.Links() {
		spanData.Links[i] = SpanContext{
			TraceID: TraceID{link.SpanContext.TraceID()},
			SpanID:  SpanID{link.SpanContext.SpanID()},
		}
	}

	// populate snapshot from otel attributes
	for _, attr := range span.Attributes() {
		spanData.ProcessAttribute(
			string(attr.Key),
			attr.Value.AsInterface(),
		)
	}

	// integrate the span's data into the DB's live objects
	db.integrateSpan(spanData)
}

type Activity struct {
	CompletedIntervals []Interval
	EarliestRunning    time.Time
	EarliestRunningID  SpanID
	// FIXME: finish
	allRunning SpanSet
}

func (activity *Activity) Intervals(now time.Time) func(func(Interval) bool) { // TODO go 1.23 iter.Seq[Interval] {
	return func(yield func(Interval) bool) {
		var lastIval *Interval
		for _, ival := range activity.CompletedIntervals {
			ival := ival
			lastIval = &ival
			if !activity.EarliestRunning.IsZero() &&
				activity.EarliestRunning.Before(ival.Start) {
				yield(Interval{Start: activity.EarliestRunning, End: now})
				break
			}
			if !yield(ival) {
				return
			}
		}
		if !activity.EarliestRunning.IsZero() &&
			(lastIval == nil || activity.EarliestRunning.After(lastIval.End)) {
			yield(Interval{Start: activity.EarliestRunning, End: now})
		}
	}
}

func (activity *Activity) Duration(now time.Time) time.Duration {
	var dur time.Duration
	// TODO go 1.23
	// for ival := range activity.Intervals(now) {
	// 	dur += ival.End.Sub(ival.Start)
	// }
	activity.Intervals(now)(func(ival Interval) bool {
		dur += ival.End.Sub(ival.Start)
		return true
	})
	return dur
}

type Interval struct {
	Start time.Time
	End   time.Time
}

func (activity *Activity) Add(span *Span) {
	if span.IsRunning() {
		if activity.EarliestRunning.IsZero() ||
			span.StartTime.Before(activity.EarliestRunning) {
			// FIXME: we need to also keep track of the other running ones
			// incase the earliest one ends
			activity.EarliestRunning = span.StartTime
			activity.EarliestRunningID = span.ID
		}
		return
	}

	if activity.EarliestRunningID == span.ID {
		activity.EarliestRunning = time.Time{}
		activity.EarliestRunningID = SpanID{}
	}

	ival := Interval{
		Start: span.StartTime,
		End:   span.EndTime,
	}

	if len(activity.CompletedIntervals) == 0 {
		activity.CompletedIntervals = append(activity.CompletedIntervals, ival)
		return
	}

	idx, _ := slices.BinarySearchFunc(activity.CompletedIntervals, ival, func(a, b Interval) int {
		if a.Start.Before(b.Start) {
			return -1
		} else if a.Start.After(b.Start) {
			return 1
		} else {
			return 0
		}
	})
	// slog.Warn("inserting interval", "idx", idx, "ival", ival, "match", match)

	activity.CompletedIntervals = slices.Insert(activity.CompletedIntervals, idx, ival)

	activity.mergeIntervals()
}

// mergeIntervals merges overlapping intervals in the activity.
func (activity *Activity) mergeIntervals() {
	merged := []Interval{}
	var lastIval *Interval
	for _, ival := range activity.CompletedIntervals {
		ival := ival
		if lastIval == nil {
			merged = append(merged, ival)
			lastIval = &merged[len(merged)-1]
			continue
		}
		if ival.Start.Before(lastIval.End) {
			if ival.End.After(lastIval.End) {
				// extend
				lastIval.End = ival.End
			} else {
				// wholly subsumed; skip
				continue
			}
		} else {
			merged = append(merged, ival)
			lastIval = &merged[len(merged)-1]
		}
	}
	activity.CompletedIntervals = merged
}

// integrateSpan takes a possibly newly created span and updates
// database relationships and state
func (db *DB) integrateSpan(span *Span) {
	// track the span's own interval
	span.Activity.Add(span)
	db.updatedSpans.Add(span)

	// keep track of the time boundary
	if db.Epoch.IsZero() ||
		(!span.StartTime.IsZero() &&
			span.StartTime.Before(db.Epoch)) {
		db.Epoch = span.StartTime
	}
	if span.EndTime.After(db.End) {
		db.End = span.EndTime
	}

	// associate the span to its parents and links
	if span.ParentID.IsValid() &&
		// If a span has links, don't bother associating it to its
		// parent. We might want to use that info someday (the
		// "unlazying" point), but no use case right now.
		len(span.Links) == 0 {
		span.ParentSpan = db.initSpan(span.ParentID)
		span.ParentSpan.ChildSpans.Add(span)
	}
	for _, linkedCtx := range span.Links {
		linked := db.initSpan(linkedCtx.SpanID)
		linked.ChildSpans.Add(span)
		linked.LinkedFrom.Add(span)
		span.LinksTo.Add(linked)
	}

	// update span states, propagating them up through parents, too
	span.PropagateStatusToParentsAndLinks()

	// keep track of intervals seen for a digest
	if span.CallDigest != "" {
		if db.Intervals[span.CallDigest] == nil {
			db.Intervals[span.CallDigest] = make(map[time.Time]*Span)
		}
		db.Intervals[span.CallDigest][span.StartTime] = span
	}

	if span.Call == nil && span.CallPayload != "" {
		var call callpbv1.Call
		if err := call.Decode(span.CallPayload); err != nil {
			slog.Warn("failed to decode id", "err", err)
		} else {
			span.Call = &call

			// Seeing loadFooFromID is only really interesting if it actually
			// resulted in evaluating the ID, so we set Passthrough, which will only
			// show its children.
			if call.Field == fmt.Sprintf("load%sFromID", call.Type.ToAST().Name()) {
				span.Passthrough = true
			}

			// We also don't care about seeing the id field selection itself, since
			// it's more noisy and confusing than helpful. We'll still show all the
			// spans leading up to it, just not the ID selection.
			if call.Field == "id" {
				span.Ignore = true
			}

			// We don't care about seeing the sync span itself - all relevant info
			// should show up somewhere more familiar.
			if call.Field == "sync" {
				span.Ignore = true
			}

			if span.CallDigest != "" {
				db.Calls[span.CallDigest] = &call
			}
		}
	}

	// TODO: respect an already-set base value computed server-side, and client
	// subsequently requests necessary DAG
	if span.Call != nil && span.Call.ReceiverDigest != "" {
		parentCall, ok := db.Calls[span.Call.ReceiverDigest]
		if ok {
			span.Base = db.Simplify(parentCall, span.Internal)
		}
	}

	if !span.ParentID.IsValid() {
		// TODO: when we initialize new spans we haven't seen before, they
		// end up with a zero parent ID, so just do a nil check as a
		// workaround, as we'll always see the true root first.
		if db.RootSpan == nil {
			// keep track of the trace's root span
			db.RootSpan = span
		}

		if !db.PrimarySpan.IsValid() {
			// default primary to root span, though we might never see a "root
			// span" in a nested scenario.
			db.PrimarySpan = span.ID
		}
	}

	if span.EffectID != "" {
		if db.EffectSpans[span.EffectID] == nil {
			db.EffectSpans[span.EffectID] = NewSpanSet()
		}
		db.EffectSpans[span.EffectID].Add(span)
		if span.IsFailed() {
			db.FailedEffects[span.EffectID] = true
		}
	}

	for _, dig := range span.EffectsCompleted {
		db.CompletedEffects[dig] = true
	}

	if span.CallDigest != "" {
		// parent -> child
		if db.Outputs[span.CallDigest] == nil {
			db.Outputs[span.CallDigest] = make(map[string]struct{})
		}
		db.Outputs[span.CallDigest][span.Output] = struct{}{}

		// child -> parent
		if db.OutputOf[span.Output] == nil {
			db.OutputOf[span.Output] = make(map[string]struct{})
		}
		db.OutputOf[span.Output][span.CallDigest] = struct{}{}
	}

	for _, id := range span.EffectIDs {
		if db.CauseSpans[id] == nil {
			db.CauseSpans[id] = NewSpanSet()
		}
		db.CauseSpans[id].Add(span)
	}

	// finally, install the span if we don't already have it
	//
	// this dance is a little clumsy because we want to make sure parent spans
	// are inserted before their child spans, so we find-or-allocate the span but
	// aggressively initialize its parent span
	//
	// FIXME: refactor? can we keep some sort of flat map of spans an append
	// children to them instead of having the single big ordered list?
	if db.Spans.Map[span.ID] != span {
		db.Spans.Add(span)
	}
}

func (db *DB) HighLevelSpan(call *callpbv1.Call) *Span {
	return db.MostInterestingSpan(db.Simplify(call, false).Digest)
}

func (db *DB) MostInterestingSpan(dig string) *Span {
	var earliest *Span
	var earliestCached bool
	vs := make([]*Span, 0, len(db.Intervals[dig]))
	for _, span := range db.Intervals[dig] {
		vs = append(vs, span)
	}
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].StartTime.Before(vs[j].StartTime)
	})
	for _, span := range db.Intervals[dig] {
		// a running vertex is always most interesting, and these are already in
		// order
		if span.IsRunningOrLinksRunning() {
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
		case span.StartTime.Before(earliest.StartTime):
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
			Field: "no",
			Type: &callpbv1.Type{
				NamedType: "Missing",
			},
			Args: []*callpbv1.Argument{
				{
					Name: "digest",
					Value: &callpbv1.Literal{
						Value: &callpbv1.Literal_String_{
							String_: dig,
						},
					},
				},
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

func (db *DB) Simplify(call *callpbv1.Call, force bool) (smallest *callpbv1.Call) {
	smallest = call
	smallestSize := -1
	if !force {
		smallestSize = db.idSize(smallest)
	}

	creators, ok := db.OutputOf[call.Digest]
	if !ok {
		return smallest
	}
	simplified := false

loop:
	for creatorDig := range creators {
		if creatorDig == call.Digest {
			// can't be simplified to itself
			continue
		}
		creator, ok := db.Calls[creatorDig]
		if ok {
			for _, creatorArg := range creator.Args {
				if creatorArg, ok := creatorArg.Value.Value.(*callpbv1.Literal_CallDigest); ok {
					if creatorArg.CallDigest == call.Digest {
						// can't be simplified to a call that references itself
						// in it's argument - which would loop endlessly
						continue loop
					}
				}
			}

			if size := db.idSize(creator); smallestSize == -1 || size < smallestSize {
				smallest = creator
				smallestSize = size
				simplified = true
			}
		}
	}
	if simplified {
		return db.Simplify(smallest, false)
	}
	return smallest
}

func getAttr(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

// Function to check if a row is or contains a target row
func isOrContains(row, target *TraceTree) bool {
	if row == target {
		return true
	}
	for _, child := range row.Children {
		if isOrContains(child, target) {
			return true
		}
	}
	return false
}

func WalkTree(tree []*TraceTree, f func(*TraceTree, int) bool) {
	var walk func([]*TraceTree, int)
	walk = func(rows []*TraceTree, depth int) {
		for _, row := range rows {
			if f(row, depth) {
				return
			}
			walk(row.Children, depth+1)
		}
	}
	walk(tree, 0)
}

func (db *DB) CollectErrors(rows *RowsView) []*TraceTree {
	reveal := make(map[*TraceTree]struct{})
	var collect func(row *TraceTree)

	collect = func(row *TraceTree) {
		if !row.Span.IsFailedOrCausedFailure() {
			return
		}
		reveal[row] = struct{}{}
		for _, child := range row.Children {
			collect(child)
		}
	}

	for _, row := range rows.Body {
		collect(row)
	}

	return collectParents(rows.Body, reveal)
}

func collectParents(rows []*TraceTree, targets map[*TraceTree]struct{}) []*TraceTree {
	var result []*TraceTree

	for _, row := range rows {
		contains := false
		for target := range targets {
			if isOrContains(row, target) {
				contains = true
				break
			}
		}
		if !contains {
			continue
		}
		rowCopy := *row
		rowCopy.Children = collectParents(row.Children, targets)
		result = append(result, &rowCopy)
	}

	return result
}
