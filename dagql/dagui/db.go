package dagui

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
)

type DB struct {
	PrimarySpan SpanID
	PrimaryLogs map[SpanID][]sdklog.Record

	Epoch, End time.Time

	Spans    *OrderedSet[SpanID, *Span]
	RootSpan *Span

	Resources map[attribute.Distinct]*resource.Resource

	Calls     map[string]*callpbv1.Call
	Outputs   map[string]map[string]struct{}
	OutputOf  map[string]map[string]struct{}
	Intervals map[string]map[time.Time]*Span

	CauseSpans  map[string]SpanSet
	EffectSpans map[string]SpanSet

	CompletedEffects map[string]bool
	FailedEffects    map[string]bool

	// Map of call digest -> metric name -> data points
	// NOTE: this is hard coded for Gauge int64 metricdata essentially right now,
	// needs generalization as more metric types get added
	MetricsByCall map[string]map[string][]metricdata.DataPoint[int64]

	// updatedSpans is a set of spans that have been updated since the last
	// sync, which includes any parent spans whose overall active time intervals
	// or status were modified via a child or linked span.
	updatedSpans SpanSet

	// seenSpans keeps track of which spans have been observed via
	// UpdatedSnapshots so that we can know whether we need to send them when we
	// finally see them
	seenSpans map[SpanID]struct{}
}

func NewDB() *DB {
	return &DB{
		PrimaryLogs: make(map[SpanID][]sdklog.Record),

		Spans:     NewSpanSet(),
		Resources: make(map[attribute.Distinct]*resource.Resource),

		Calls:     make(map[string]*callpbv1.Call),
		OutputOf:  make(map[string]map[string]struct{}),
		Outputs:   make(map[string]map[string]struct{}),
		Intervals: make(map[string]map[time.Time]*Span),

		CompletedEffects: make(map[string]bool),
		FailedEffects:    make(map[string]bool),
		CauseSpans:       make(map[string]SpanSet),
		EffectSpans:      make(map[string]SpanSet),

		updatedSpans: NewSpanSet(),
		seenSpans:    make(map[SpanID]struct{}),
	}
}

func (db *DB) seen(spanID SpanID) {
	db.seenSpans[spanID] = struct{}{}
}

func (db *DB) hasSeen(spanID SpanID) bool {
	_, seen := db.seenSpans[spanID]
	return seen
}

func (db *DB) UpdatedSnapshots(filter map[SpanID]bool) []SpanSnapshot {
	snapshots := snapshotSpans(db.updatedSpans.Order, func(span *Span) bool {
		if filter == nil || filter[span.ParentID] {
			// include subscribed (or all) spans
			return true
		}
		if span.IsFailedOrCausedFailure() {
			// include failed spans so we can summarize them without having to
			// deep-dive.
			return true
		}
		if span.Passthrough {
			// include any passthrough spans to ensure failures are collected.
			// the POST /query span for example never fails on its own.
			for _, child := range span.ChildSpans.Order {
				if child.IsFailedOrCausedFailure() {
					return true
				}
			}
		}
		return false
	})
	for spanID := range filter {
		span := db.Spans.Map[spanID]
		if span == nil {
			continue
		}
		if !db.hasSeen(spanID) {
			snapshots = append(snapshots, span.Snapshot())
		}
		for p := range span.Parents {
			if !db.hasSeen(p.ID) {
				snapshots = append(snapshots, p.Snapshot())
			}
		}
	}
	for _, snapshot := range snapshots {
		db.seen(snapshot.ID)
	}
	db.updatedSpans = NewSpanSet()
	return snapshots
}

func (db *DB) ImportSnapshots(snapshots []SpanSnapshot) {
	for _, snapshot := range snapshots {
		span := db.findOrAllocSpan(snapshot.ID)
		span.Received = true
		snapshot.Version += span.Version // don't reset the version
		span.SpanSnapshot = snapshot
		db.integrateSpan(span)
	}
}

func (db *DB) update(span *Span) {
	if span.Final {
		// don't bump versions for final spans; leave the remote as the
		// source of truth, lest we stray forward and miss an actual version bump
		return
	}
	span.Version++
	db.updatedSpans.Add(span)
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
	snaps := snapshotSpans(db.Spans.Order, func(span *Span) bool {
		return span.ParentID == id || span.ID == id
	})
	if span := db.Spans.Map[id]; span != nil {
		for p := range span.Parents {
			if !db.hasSeen(p.ID) {
				snaps = append(snaps, p.Snapshot())
			}
		}
	}
	for _, snapshot := range snaps {
		db.seen(snapshot.ID)
	}
	return snaps
}

func (db *DB) RemainingSnapshots() []SpanSnapshot {
	return snapshotSpans(db.Spans.Order, func(span *Span) bool {
		return !db.hasSeen(span.ID)
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

func (db *DB) MetricExporter() sdkmetric.Exporter {
	return DBMetricExporter{db}
}

func (db *DB) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (db *DB) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

type DBMetricExporter struct {
	*DB
}

func (db DBMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	for _, scopeMetric := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			metricData, ok := metric.Data.(metricdata.Gauge[int64])
			if !ok {
				continue
			}

			for _, point := range metricData.DataPoints {
				callDigest, ok := point.Attributes.Value(telemetry.DagDigestAttr)
				if !ok {
					continue
				}

				if db.MetricsByCall == nil {
					db.MetricsByCall = make(map[string]map[string][]metricdata.DataPoint[int64])
				}
				metricsByName, ok := db.MetricsByCall[callDigest.AsString()]
				if !ok {
					metricsByName = make(map[string][]metricdata.DataPoint[int64])
					db.MetricsByCall[callDigest.AsString()] = metricsByName
				}
				metricsByName[metric.Name] = append(metricsByName[metric.Name], point)
			}
		}
	}

	return nil
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
		ChildSpans:      NewSpanSet(),
		RunningSpans:    NewSpanSet(),
		FailedLinks:     NewSpanSet(),
		causesViaLinks:  NewSpanSet(),
		effectsViaLinks: NewSpanSet(),
		effectsViaAttrs: map[string]SpanSet{},
		db:              db,
	}
}

func (db *DB) recordOTelSpan(span sdktrace.ReadOnlySpan) {
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

	if resource := span.Resource(); resource != nil {
		db.Resources[resource.Equivalent()] = resource
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

	// Keep track of the full set of running spans so we can update
	// EarliestRunning as they complete.
	//
	// This needs to be synced to the frontend so it doesn't lose track of the
	// running status in updateEarliest. We exclude from JSON marshalling since
	// the map key is incompatible. Syncing to the frontend uses encoding/gob,
	// which accepts the map key type.
	AllRunning map[SpanID]time.Time `json:"-"`
}

func (activity *Activity) Intervals(now time.Time) iter.Seq[Interval] {
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
	for ival := range activity.Intervals(now) {
		dur += ival.End.Sub(ival.Start)
	}
	return dur
}

type Interval struct {
	Start time.Time
	End   time.Time
}

func (activity *Activity) Add(span *Span) bool {
	var changed bool

	if span.IsRunning() {
		if activity.AllRunning == nil {
			activity.AllRunning = map[SpanID]time.Time{}
		}
		if _, found := activity.AllRunning[span.ID]; !found {
			activity.AllRunning[span.ID] = span.StartTime
			changed = true
		}
		if activity.updateEarliest() {
			changed = true
		}
		return changed
	}

	delete(activity.AllRunning, span.ID)
	if activity.updateEarliest() {
		changed = true
	}

	ival := Interval{
		Start: span.StartTime,
		End:   span.EndTime,
	}

	if len(activity.CompletedIntervals) == 0 {
		activity.CompletedIntervals = append(activity.CompletedIntervals, ival)
		changed = true
		return changed
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

	// optimization: if the new interval is wholly subsumed by an existing
	// interval we can skip adding it. this is also handled by mergeIntervals,
	// but it's harder to return false after the fact.
	for _, existing := range activity.CompletedIntervals {
		if ival.Start.After(existing.Start) && ival.End.Before(existing.End) {
			return changed
		}
	}

	activity.CompletedIntervals = slices.Insert(activity.CompletedIntervals, idx, ival)
	activity.mergeIntervals()
	changed = true
	return changed
}

func (activity *Activity) IsRunning() bool {
	return !activity.EarliestRunning.IsZero()
}

func (activity *Activity) EndTimeOrFallback(now time.Time) time.Time {
	if !activity.EarliestRunning.IsZero() {
		return now
	}
	if len(activity.CompletedIntervals) == 0 {
		return time.Time{}
	}
	return activity.CompletedIntervals[len(activity.CompletedIntervals)-1].End
}

func (activity *Activity) updateEarliest() (changed bool) {
	if len(activity.AllRunning) > 0 {
		for _, t := range activity.AllRunning {
			if activity.EarliestRunning.IsZero() || t.Before(activity.EarliestRunning) {
				activity.EarliestRunning = t
				changed = true
			}
		}
	} else if !activity.EarliestRunning.IsZero() {
		activity.EarliestRunning = time.Time{}
		changed = true
	}
	return
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
func (db *DB) integrateSpan(span *Span) { //nolint: gocyclo
	// track the span's own interval
	span.Activity.Add(span)
	db.update(span)

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
		if span.ParentSpan.ChildSpans.Add(span) {
			// if we're a new child, take a new snapshot for ChildCount
			db.update(span.ParentSpan)
		}
	}
	for _, linkedCtx := range span.Links {
		linked := db.initSpan(linkedCtx.SpanID)
		linked.ChildSpans.Add(span)
		linked.effectsViaLinks.Add(span)
		span.causesViaLinks.Add(linked)
	}

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

			if span.CallDigest != "" {
				db.Calls[span.CallDigest] = &call
			}
		}
	}

	if call := span.Call; call != nil {
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
			span.Passthrough = true
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
		causes := db.CauseSpans[span.EffectID]
		if causes == nil {
			causes = NewSpanSet()
			db.CauseSpans[span.EffectID] = causes
		}
		span.causesViaAttrs = causes
	}

	for _, id := range span.EffectIDs {
		effects := db.EffectSpans[id]
		if effects == nil {
			effects = NewSpanSet()
			db.EffectSpans[id] = effects
		}
		span.effectsViaAttrs[id] = effects
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

	// update span states, propagating them up through parents, too
	span.PropagateStatusToParentsAndLinks()

	// finally, install the span if we don't already have it
	//
	// this dance is a little clumsy because we want to make sure parent spans
	// are inserted before their child spans, so we find-or-allocate the span but
	// aggressively initialize its parent span
	//
	// FIXME: refactor? can we keep some sort of flat map of spans an append
	// children to them instead of having the single big ordered list?
	db.Spans.Add(span)
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
		if span.IsRunningOrEffectsRunning() {
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
	collect = func(tree *TraceTree) {
		failed := tree.Span.IsFailedOrCausedFailure()
		if failed {
			reveal[tree] = struct{}{}
		}
		if failed || tree.Span.IsUnset() {
			for _, child := range tree.Children {
				collect(child)
			}
		}
	}

	for _, row := range rows.Body {
		collect(row)
	}

	return collectParents(rows.Body, reveal)
}

func (db *DB) FindResource(filter attribute.KeyValue) *resource.Resource {
	for _, res := range db.Resources {
		for _, kv := range res.Attributes() {
			if kv.Key == filter.Key && kv.Value == filter.Value {
				return res
			}
		}
	}
	return nil
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
