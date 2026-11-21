package dagui

import (
	"context"
	"encoding/binary"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
)

// Fold-on-ingest (see hack/designs/fold-on-ingest.md).
//
// A DB built WithFold keeps in full (a *Span) only the spans it may render:
// the RETAINED spans, which a FoldRetain policy picks as they arrive, and
// the ancestors of every retained span. Every other span is FOLDED: all the
// DB keeps of it is a stub of ~50 bytes -- its place in the tree, its
// interval, its interned name, its last roll-up category and a few flags --
// and its contribution to the aggregates
// its retained ancestors show: roll-up counts, child counts, has-logs, and
// whether something beneath them is running. A folded span is read back in
// full (FoldOptions.Load) when something needs it: a retained descendant, or
// a row expanded to show its children (Fold.Expand), whose children are
// retained from then on, those still to come included.
//
// It's opt-in, for frontends that render a part of a trace they receive
// whole (the local CLI, the engine's trace tools). Folding is not demoted:
// what was retained stays.
//
// A retained row's waiting below it reads its folded children's intervals
// from their stubs (foldedWork), and the spans that waiting needs in full
// are retained whatever the policy (neededForWaits), so its time breakdown
// is the one a full DB computes; a wait that provably tells no row anything
// retains nothing (contained waits, fold_contain.go).

// foldState packs a stub's state: its roll-up category in the low 3 bits,
// then flags.
type foldState uint32

const (
	foldCatMask foldState = 0x7 // spanStateCategory

	foldReceived foldState = 1 << (3 + iota)
	foldRetained
	// foldFull marks a retained span the DB has received in full since it
	// was retained: a read-back answer must not overwrite it.
	foldFull
	foldBoundary
	foldEncapsulate
	foldEncapsulated
	// foldUIPassthrough is dagui's UI passthrough flag (Span.Passthrough).
	foldUIPassthrough
	foldInternal
	foldRollUpLogs
	foldRollUpSpans
	foldLLMTool
	foldHasLogs
	// foldKind marks a kind dagui surfaces wherever it sits: LLM messages
	// and tool calls, checks, generators, tests, services, rewind markers.
	foldKind
	foldAgent
	foldReveal
	foldProgress
	foldCauseLinks
	foldWaitLinks
	// foldExpanded marks a span whose children are all retained, those to
	// come included: a root, or a row expanded.
	foldExpanded
	// foldOpenAll marks a span everything beneath which is retained.
	foldOpenAll
	// foldLoading marks a span asked of FoldOptions.Load and not received
	// in full since.
	foldLoading
	// foldRunCounted marks a folded running span counted as running beneath
	// its nearest retained ancestor (foldAgg.running): one the view would
	// place, so that it shows its ancestors running.
	foldRunCounted
	// foldFunctionPlumbing is Span.FunctionPlumbing; foldExecOp marks an
	// exec (telemetryattrs.WcprofOpKindAttr). Waiting below a row that
	// runs a module function reads the execs beneath its plumbing (see
	// functionProcess), so they're retained.
	foldFunctionPlumbing
	foldExecOp
	// foldContainedWaits marks a span with contained waits (see
	// containedWait), whose targets Fold.reads lists; foldWaitsBroken a
	// span whose waits stopped being contained, all retained since.
	foldContainedWaits
	foldWaitsBroken
	// foldEscapes marks a span an edge leaves the subtree of; foldRead a
	// span whose own waits a row that doesn't hold it reads.
	foldEscapes
	foldRead
)

func (s foldState) cat() spanStateCategory { return spanStateCategory(s & foldCatMask) }

// FoldRetain is a fold policy: it reports whether a span that isn't
// retained yet must be, now that it arrived or changed. The DB retains its
// ancestors with it. A span is also retained, whatever the policy, when its
// parent is expanded (Fold.Expand), and the targets of a retained span's
// cause, error-origin and wait links are.
type FoldRetain func(span FoldSpan) bool

// FoldOptions configure fold-on-ingest (WithFold).
type FoldOptions struct {
	// Retain is the policy; nil retains what the live CLI may render (see
	// RetainRendered).
	Retain FoldRetain
	// Load asks for the latest snapshots of folded spans the DB now
	// retains, as a frontend reads them from the trace's store. It's called
	// at the end of a batch, and may answer then or later, by passing them
	// to ExportSpans or ImportSnapshots with a context from FoldFill. Nil
	// reads nothing back: the spans are retained when they next arrive.
	Load func(ids []SpanID)
	// Foldable reports whether the spans of a trace may fold: those Load
	// can read back. Nil folds every trace's.
	Foldable func(trace TraceID) bool
	// NoFoldedCalls keeps no call for folded spans (measurement only).
	NoFoldedCalls bool
}

// WithFold builds a DB that folds the spans opts.Retain doesn't retain (see
// Fold). Implies WithoutUpdateTracking: a folding DB serves no remote
// frontend.
func WithFold(opts FoldOptions) DBOption {
	return func(db *DB) {
		db.untracked = true
		if opts.Retain == nil {
			opts.Retain = RetainRendered
		}
		db.fold = &Fold{db: db, opts: opts, ingesting: noSpan}
	}
}

// Fold returns the DB's fold-on-ingest state, nil unless built WithFold.
func (db *DB) Fold() *Fold {
	return db.fold
}

type foldFillKey struct{}

// FoldFill marks ctx as carrying the answer to a FoldOptions.Load: a span
// the DB received in full since it asked is not overwritten by it, as the
// stream it receives delivers snapshots newer than the read's.
func FoldFill(ctx context.Context) context.Context {
	return context.WithValue(ctx, foldFillKey{}, true)
}

func isFoldFill(ctx context.Context) bool {
	fill, _ := ctx.Value(foldFillKey{}).(bool)
	return fill
}

// Fold is the stub table of a DB built WithFold: a stub per span received
// or named, struct-of-arrays, indexed by a dense uint32.
type Fold struct {
	db   *DB
	opts FoldOptions

	ids   foldIDTable
	spans []SpanID
	// idx+1 of the parent, first child and next sibling; 0 for none.
	parent, firstChild, nextSib []uint32
	state                       []foldState
	// start and end are each span's interval (Unix nanoseconds; end 0 while
	// running): what a retained row's waiting below it reads of the folded
	// spans beneath it (see foldedWork).
	start, end []int64
	// name is each span's name, interned in nameTab (0 for none), so that a
	// name search finds folded spans (MatchNames).
	name     []uint32
	nameTab  []string
	nameIdxs map[string]uint32

	// agg holds, per retained span, what's folded beneath it.
	agg map[uint32]*foldAgg

	// load collects the spans to read back at the end of a batch.
	load []SpanID
	// touched collects the retained spans whose aggregates a batch changed.
	touched map[uint32]struct{}
	// rootEnded is set once a root completed, at rootEnd: running spans are
	// canceled, ending then.
	rootEnded bool
	rootEnd   int64

	// Loads counts the spans asked of FoldOptions.Load.
	Loads int
	// keptCalls counts the calls kept for folded spans.
	keptCalls int

	// reads lists, for each span with waits on spans beneath it, those
	// targets: its contained waits' while it has any (see containedWait).
	reads map[uint32][]uint32
	// ingesting is the span ingest is at.
	ingesting uint32
	// Broken counts the spans whose contained waits a later edge broke,
	// and BrokenTargets the targets that were read back for it.
	Broken, BrokenTargets int
}

// foldAgg is what's folded beneath a retained span.
//
// The folded spans whose nearest retained ancestor a span is are, for the
// DB, like one more span beneath it: own sums them, and they're counted on
// the span and on every span its roll-up counts reach (rollUpAncestors),
// causes included, as each span the DB holds is.
type foldAgg struct {
	own foldCounts
	// beneath counts the folded spans counted on it, as own's are on the
	// spans they reach: at any depth, and through cause links.
	beneath int32
	// children counts its folded children.
	children int32
	// running counts the folded running spans whose nearest retained
	// ancestor it is.
	running int32
}

func (f *Fold) aggOf(i uint32) *foldAgg {
	if f.agg == nil {
		f.agg = map[uint32]*foldAgg{}
	}
	a := f.agg[i]
	if a == nil {
		a = &foldAgg{}
		f.agg[i] = a
	}
	return a
}

// FoldSpan is a span as a fold policy sees it: its stub.
type FoldSpan struct {
	f *Fold
	i uint32
}

func (s FoldSpan) has(flags foldState) bool { return s.f.state[s.i]&flags != 0 }

// ID is the span's ID.
func (s FoldSpan) ID() SpanID { return s.f.spans[s.i] }

// Parent returns the span's parent, if it named one.
func (s FoldSpan) Parent() (FoldSpan, bool) {
	p := s.f.parent[s.i]
	return FoldSpan{s.f, p - 1}, p != 0
}

func (s FoldSpan) Received() bool     { return s.has(foldReceived) }
func (s FoldSpan) Retained() bool     { return s.has(foldRetained) }
func (s FoldSpan) Expanded() bool     { return s.has(foldExpanded) }
func (s FoldSpan) Boundary() bool     { return s.has(foldBoundary) }
func (s FoldSpan) Encapsulate() bool  { return s.has(foldEncapsulate) }
func (s FoldSpan) Encapsulated() bool { return s.has(foldEncapsulated) }
func (s FoldSpan) Passthrough() bool  { return s.has(foldUIPassthrough) }
func (s FoldSpan) Internal() bool     { return s.has(foldInternal) }
func (s FoldSpan) RollUpLogs() bool   { return s.has(foldRollUpLogs) }
func (s FoldSpan) RollUpSpans() bool  { return s.has(foldRollUpSpans) }
func (s FoldSpan) LLMTool() bool      { return s.has(foldLLMTool) }
func (s FoldSpan) HasLogs() bool      { return s.has(foldHasLogs) }
func (s FoldSpan) Agent() bool        { return s.has(foldAgent) }
func (s FoldSpan) Reveal() bool       { return s.has(foldReveal) }
func (s FoldSpan) Progress() bool     { return s.has(foldProgress) }
func (s FoldSpan) CauseLinks() bool   { return s.has(foldCauseLinks) }
func (s FoldSpan) WaitLinks() bool    { return s.has(foldWaitLinks) }

// Name is the span's name.
func (s FoldSpan) Name() string {
	if n := s.f.name[s.i]; n != 0 {
		return s.f.nameTab[n]
	}
	return ""
}

// SurfacedKind reports whether the span is of a kind dagui surfaces
// wherever it sits (see Span.IsSurfacedKind), or a rewind marker.
func (s FoldSpan) SurfacedKind() bool { return s.has(foldKind | foldAgent) }

// Failed reports whether the span failed.
func (s FoldSpan) Failed() bool { return s.f.state[s.i].cat() == stateFailed }

// Running reports whether the span is running.
func (s FoldSpan) Running() bool { return s.f.state[s.i].cat() == stateRunning }

// MayRollUp is spanMayRollUp over stubs, against the whole trace: no
// boundary or encapsulating span above it. An ancestor not received yet
// can't be ruled out, so it doesn't contain.
func (s FoldSpan) MayRollUp() bool {
	for p, ok := s.Parent(); ok; p, ok = p.Parent() {
		if p.has(foldBoundary | foldEncapsulate) {
			return false
		}
	}
	return true
}

// RetainRendered is the live CLI's fold policy: what it may render. Spans
// are retained when they're top-level rows (see Fold), agents always, and
// what may surface -- surfaced kinds wherever they sit (a conversation's
// tool call shows the checks and tests the nested engine beneath it ran),
// failed, revealed and progress-carrying spans that may roll up, and spans
// with cause links -- with their ancestors. A failure beneath a boundary or
// encapsulation (e.g. a nested engine's expected failure) only counts in its
// ancestors' roll-ups.
func RetainRendered(span FoldSpan) bool {
	if span.Agent() || span.CauseLinks() || span.SurfacedKind() {
		return true
	}
	if !span.Failed() && !span.Reveal() && !span.Progress() {
		return false
	}
	return span.MayRollUp()
}

func (f *Fold) intern(id SpanID) uint32 {
	if i, ok := f.ids.get(id, f.spans); ok {
		return i
	}
	i := uint32(len(f.spans))
	f.spans = append(f.spans, id)
	f.parent = append(f.parent, 0)
	f.firstChild = append(f.firstChild, 0)
	f.nextSib = append(f.nextSib, 0)
	f.state = append(f.state, 0)
	f.start = append(f.start, 0)
	f.end = append(f.end, 0)
	f.name = append(f.name, 0)
	f.ids.put(id, i, f.spans)
	return i
}

// setName records stub i's name.
func (f *Fold) setName(i uint32, name string) {
	if name == "" {
		f.name[i] = 0
		return
	}
	n, ok := f.nameIdxs[name]
	if !ok {
		if f.nameIdxs == nil {
			f.nameIdxs = map[string]uint32{}
			f.nameTab = []string{""}
		}
		n = uint32(len(f.nameTab))
		name = strings.Clone(name)
		f.nameTab = append(f.nameTab, name)
		f.nameIdxs[name] = n
	}
	f.name[i] = n
}

// MatchNames returns the folded spans whose name match accepts, up to
// limit (0 for all), and how many there are. Each distinct name is matched
// once.
func (f *Fold) MatchNames(match func(name string) bool, limit int) (ids []SpanID, total int) {
	if f == nil || len(f.nameTab) == 0 {
		return nil, 0
	}
	matches := make([]bool, len(f.nameTab))
	for n, name := range f.nameTab {
		matches[n] = n != 0 && match(name)
	}
	for i, st := range f.state {
		if st&(foldReceived|foldRetained) != foldReceived || !matches[f.name[i]] {
			continue
		}
		total++
		if limit == 0 || len(ids) < limit {
			ids = append(ids, f.spans[i])
		}
	}
	return ids, total
}

// noteName records a folded span's name, updated by a log record (see
// ingestSpanName), reporting false when the DB is to do it, as it retains
// the span.
func (f *Fold) noteName(id SpanID, name string) bool {
	i := f.intern(id)
	if f.state[i]&foldRetained != 0 {
		return false
	}
	f.setName(i, name)
	return true
}

func (f *Fold) lookup(id SpanID) (uint32, bool) {
	return f.ids.get(id, f.spans)
}

// foldInfo is what the fold reads of an arriving span.
type foldInfo struct {
	id, parent SpanID
	trace      TraceID
	flags      foldState
	cat        spanStateCategory
	start, end time.Time
	name       string
	// callDigest and callPayload are the call the span carries, if any.
	callDigest, callPayload string
	// linked holds the targets of its cause and error-origin links, which
	// are retained with it; causes those of its cause links, and waits
	// those of its wait links.
	linked, causes, waits []SpanID
	// origins holds the error origins its status names.
	failed bool
}

// decodedSpan is a span whose attributes and links were read once, for the
// fold and then the DB: a span read from a store decodes them on every
// call.
type decodedSpan struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
	links []sdktrace.Link
}

func (s decodedSpan) Attributes() []attribute.KeyValue { return s.attrs }
func (s decodedSpan) Links() []sdktrace.Link            { return s.links }

func decodeSpan(s sdktrace.ReadOnlySpan) decodedSpan {
	if d, ok := s.(decodedSpan); ok {
		return d
	}
	return decodedSpan{s, s.Attributes(), s.Links()}
}

func otelFoldInfo(s sdktrace.ReadOnlySpan) foldInfo {
	info := foldInfo{
		id:     SpanID{s.SpanContext().SpanID()},
		parent: SpanID{s.Parent().SpanID()},
		trace:  TraceID{s.SpanContext().TraceID()},
		start:  s.StartTime(),
		end:    s.EndTime(),
		name:   s.Name(),
	}
	var pending, cached, canceled bool
	for _, attr := range s.Attributes() {
		key := string(attr.Key)
		switch key {
		case telemetry.PendingAttr:
			pending = attr.Value.AsBool()
		case telemetry.CachedAttr:
			cached = attr.Value.AsBool()
		case telemetry.CanceledAttr:
			canceled = attr.Value.AsBool()
		case telemetryattrs.UIHasLogsAttr:
			if attr.Value.AsBool() {
				info.flags |= foldHasLogs
			}
		case telemetry.DagDigestAttr:
			info.callDigest = attr.Value.AsString()
		case telemetry.DagCallAttr:
			info.callPayload = attr.Value.AsString()
		default:
			info.flags |= foldAttrFlag(key, attr.Value.AsBool(), attr.Value.AsString())
		}
	}
	info.failed = s.Status().Code == codes.Error
	info.cat = foldCategory(s.EndTime().Before(s.StartTime()), pending, cached, canceled, info.failed)
	for _, link := range s.Links() {
		purpose := ""
		for _, kv := range link.Attributes {
			if string(kv.Key) == telemetry.LinkPurposeAttr {
				purpose = kv.Value.AsString()
			}
		}
		info.addLink(purpose, SpanID{link.SpanContext.SpanID()})
	}
	if info.failed {
		info.addOrigins(s.Status().Description)
	}
	return info
}

func snapshotFoldInfo(s *SpanSnapshot) foldInfo {
	info := foldInfo{id: s.ID, parent: s.ParentID, trace: s.TraceID, start: s.StartTime, end: s.EndTime, name: s.Name,
		callDigest: s.CallDigest, callPayload: s.CallPayload}
	set := func(on bool, flag foldState) {
		if on {
			info.flags |= flag
		}
	}
	set(s.Boundary, foldBoundary)
	set(s.Encapsulate, foldEncapsulate)
	set(s.Encapsulated, foldEncapsulated)
	set(s.Passthrough, foldUIPassthrough)
	set(s.Internal, foldInternal)
	set(s.RollUpLogs, foldRollUpLogs)
	set(s.RollUpSpans, foldRollUpSpans)
	set(s.LLMTool != "", foldLLMTool)
	set(s.HasLogs, foldHasLogs)
	set(s.Agent, foldAgent)
	set(s.Reveal, foldReveal)
	set(s.FunctionPlumbing, foldFunctionPlumbing)
	if kind, _ := s.ExtraAttributes.String(telemetryattrs.WcprofOpKindAttr); kind == "exec" {
		info.flags |= foldExecOp
	}
	set(s.Progress != nil && len(s.Progress.Order) > 0, foldProgress)
	set(s.LLMRole != "" || s.CheckName != "" || s.GeneratorName != "" ||
		s.TestCaseName != "" || s.TestSuiteName != "" || s.Service || s.ServiceName != "" ||
		s.AgentRewindFrom != "" || s.AgentRewindTo != "", foldKind)
	info.failed = s.Status.Code == codes.Error || (s.Final && s.Failed_)
	info.cat = foldCategory(s.EndTime.Before(s.StartTime), s.Pending || (s.Final && s.Pending_),
		s.Cached || (s.Final && s.Cached_), s.Canceled || (s.Final && s.Canceled_), info.failed)
	for _, link := range s.Links {
		info.addLink(link.Purpose, link.SpanContext.SpanID)
	}
	if info.failed {
		info.addOrigins(s.Status.Description)
	}
	return info
}

func (info *foldInfo) addLink(purpose string, target SpanID) {
	if !target.IsValid() || target == info.id {
		return
	}
	switch purpose {
	case telemetry.LinkPurposeCause, "":
		info.flags |= foldCauseLinks
		info.causes = append(info.causes, target)
	case telemetryattrs.LinkPurposeWait:
		// retained, with its target, unless the wait is contained (see
		// Fold.containedWait): ingest decides
		info.waits = append(info.waits, target)
		return
	case telemetry.LinkPurposeErrorOrigin:
	default:
		return
	}
	info.linked = append(info.linked, target)
}

func (info *foldInfo) addOrigins(description string) {
	for _, origin := range telemetry.ParseErrorOrigins(description) {
		if id := (SpanID{SpanID: origin.SpanID()}); id.IsValid() && id != info.id {
			info.linked = append(info.linked, id)
		}
	}
}

// foldAttrFlag is the stub flag an attribute sets, if any.
func foldAttrFlag(key string, on bool, str string) foldState {
	switch key {
	case telemetry.UIBoundaryAttr:
		return boolFlag(on, foldBoundary)
	case telemetry.UIEncapsulateAttr:
		return boolFlag(on, foldEncapsulate)
	case telemetry.UIEncapsulatedAttr:
		return boolFlag(on, foldEncapsulated)
	case telemetry.UIPassthroughAttr:
		return boolFlag(on, foldUIPassthrough)
	case telemetry.UIInternalAttr:
		return boolFlag(on, foldInternal)
	case telemetry.UIRollUpLogsAttr:
		return boolFlag(on, foldRollUpLogs)
	case telemetry.UIRollUpSpansAttr:
		return boolFlag(on, foldRollUpSpans)
	case telemetry.UIRevealAttr:
		return boolFlag(on, foldReveal)
	case telemetryattrs.AgentAttr:
		return boolFlag(on, foldAgent)
	case telemetry.LLMToolAttr:
		return foldKind | foldLLMTool
	case telemetryattrs.ServiceAttr:
		return boolFlag(on, foldKind)
	case telemetry.LLMRoleAttr, telemetry.CheckNameAttr, telemetry.GeneratorNameAttr,
		string(semconv.TestCaseNameKey), string(semconv.TestSuiteNameKey),
		telemetryattrs.ServiceNameAttr,
		telemetryattrs.AgentRewindFromDigestAttr, telemetryattrs.AgentRewindToDigestAttr:
		if strings.TrimSpace(str) == "" {
			return 0
		}
		return foldKind
	case "rpc.service":
		return foldEncapsulated
	case telemetryattrs.UIFunctionPlumbingAttr:
		return boolFlag(on, foldFunctionPlumbing)
	case telemetryattrs.WcprofOpKindAttr:
		return boolFlag(str == "exec", foldExecOp)
	}
	return 0
}

func boolFlag(on bool, flag foldState) foldState {
	if on {
		return flag
	}
	return 0
}

func foldCategory(running, pending, cached, canceled, failed bool) spanStateCategory {
	switch {
	case running:
		return stateRunning
	case pending:
		return statePending
	case cached:
		return stateCached
	case canceled:
		return stateCanceled
	case failed:
		return stateFailed
	default:
		return stateSuccess
	}
}

// exportSpans is ExportSpans for a folding DB: the retained spans go on to
// the DB, the folded ones stop here.
func (f *Fold) exportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	settle := f.db.deferSpanOrder()
	defer settle()
	fill := isFoldFill(ctx)
	var keep []sdktrace.ReadOnlySpan
	for _, s := range spans {
		d := decodeSpan(s)
		info := otelFoldInfo(d)
		if f.ingest(info, fill) {
			keep = append(keep, d)
		} else {
			f.keepCall(info)
		}
	}
	if len(keep) > 0 {
		f.db.exportSpans(keep)
	}
	f.finishBatch()
	return nil
}

// importSnapshots is ImportSnapshots for a folding DB.
func (f *Fold) importSnapshots(ctx context.Context, snapshots []SpanSnapshot) {
	settle := f.db.deferSpanOrder()
	defer settle()
	fill := isFoldFill(ctx)
	var keep []SpanSnapshot
	for i := range snapshots {
		info := snapshotFoldInfo(&snapshots[i])
		if f.ingest(info, fill) {
			keep = append(keep, snapshots[i])
		} else {
			f.keepCall(info)
		}
	}
	if len(keep) > 0 {
		f.db.importSnapshots(keep)
	}
	f.finishBatch()
}

// keepCall keeps the call a folded span carries: retained spans' rows render
// their calls' receivers, and resolve digests, through calls whatever span
// carried them (see Span.Base, DB.Call).
func (f *Fold) keepCall(info foldInfo) {
	if info.callDigest == "" || info.callPayload == "" || f.opts.NoFoldedCalls {
		return
	}
	if _, ok := f.db.Calls[info.callDigest]; ok {
		return
	}
	var call callpbv1.Call
	if err := call.Decode(info.callPayload); err == nil {
		f.db.addCall(info.callDigest, &call)
		f.keptCalls++
	}
}

// ingest folds an arriving span into the stub table, reporting whether the
// DB is to ingest it in full.
func (f *Fold) ingest(info foldInfo, fill bool) bool {
	i := f.intern(info.id)
	st := f.state[i]
	if fill && st&foldFull != 0 {
		// received in full since it was asked for: newer than the read
		return false
	}
	if fill && st&foldRetained == 0 {
		// not asked for: a read of more than the DB needs
		return false
	}
	first := st&foldReceived == 0
	f.state[i] |= foldReceived | info.flags
	if first || !info.end.Before(info.start) {
		// a running span's name may have changed since it started (see
		// ingestSpanName): its repeated start snapshots don't say
		f.setName(i, info.name)
	}
	if name, ok := f.db.namesFromLog[info.id]; ok && st&foldRetained == 0 {
		if info.end.Before(info.start) {
			f.setName(i, name)
		} else {
			// its final name is its own, as the DB takes it
			delete(f.db.namesFromLog, info.id)
		}
	}
	f.start[i] = info.start.UnixNano()
	f.end[i] = 0
	if !info.end.Before(info.start) {
		f.end[i] = info.end.UnixNano()
	}
	if first && info.parent.IsValid() {
		f.link(i, f.intern(info.parent))
	}
	if f.rootEnded && info.cat == stateRunning {
		// the DB cancels a span that runs past its root's end
		info.cat = stateCanceled
		f.end[i] = f.rootEnd
	}
	if len(info.causes) > 0 || len(info.waits) > 0 {
		f.ingesting = i
		for _, cause := range info.causes {
			f.noteCause(i, f.intern(cause))
		}
		info.linked = f.classifyWaits(i, info.waits, info.linked)
		f.ingesting = noSpan
	}
	retained := f.state[i]&foldRetained != 0
	if !retained {
		f.setCategory(i, info.cat)
		if f.shouldRetain(i) || (f.opts.Foldable != nil && !f.opts.Foldable(info.trace)) {
			f.promote(i, i)
			retained = true
		}
	} else {
		f.state[i] = f.state[i]&^foldCatMask | foldState(info.cat)
	}
	if retained {
		f.state[i] = f.state[i]&^foldLoading | foldFull
		for _, target := range info.linked {
			f.promote(f.intern(target), noSpan)
		}
	}
	if first && !info.parent.IsValid() {
		// a root's children are the top-level rows
		f.expand(i)
	}
	if !info.parent.IsValid() && info.cat != stateRunning && !f.rootEnded {
		f.endRoot(f.end[i])
	}
	return retained
}

const noSpan = ^uint32(0)

func (f *Fold) shouldRetain(i uint32) bool {
	if p := f.parent[i]; p != 0 && f.state[p-1]&(foldExpanded|foldOpenAll) != 0 {
		return true
	}
	if f.neededForWaits(i) {
		return true
	}
	return f.opts.Retain(FoldSpan{f, i})
}

// neededForWaits reports whether the waiting below a retained row needs
// span i in full, whatever the policy: what folds stays exact only as plain
// work (see foldedWork). That's a span with wait links that aren't
// contained (see fold_contain.go), which tells waits that may rise; a
// module function's plumbing, whose process doesn't count
// as work; and the execs beneath plumbing, among which the process is found
// (functionProcess).
func (f *Fold) neededForWaits(i uint32) bool {
	st := f.state[i]
	if st&(foldWaitLinks|foldFunctionPlumbing) != 0 {
		return true
	}
	if st&foldExecOp == 0 {
		return false
	}
	for p := f.parent[i]; p != 0; p = f.parent[p-1] {
		if f.state[p-1]&foldFunctionPlumbing != 0 {
			return true
		}
	}
	return false
}

// foldedWork appends what span's folded children tell the waiting below it
// (the time breakdown of span's row, and UpdateRowWaits): their intervals,
// as span's painted intervals take its children's (paintedIntervals), and,
// with grandchildren, the intervals of theirs too, which is a folded child's
// work as its summary would tell it. A folded span has no wait links, no
// causes and no effects, no plumbing (see neededForWaits): its summary's
// painted intervals are its own interval and its children's, and nothing
// rises from it.
func (f *Fold) foldedWork(span *Span, clk *breakdownClock, grandchildren bool, dst []Interval) []Interval {
	if f == nil || len(f.agg) == 0 {
		return dst
	}
	i, ok := f.lookup(span.ID)
	if !ok || f.agg[i] == nil || f.agg[i].children == 0 {
		return dst
	}
	for c := f.firstChild[i]; c != 0; c = f.nextSib[c-1] {
		if f.state[c-1]&(foldRetained|foldReceived) != foldReceived {
			continue
		}
		dst = append(dst, f.observed(c-1, clk))
		if !grandchildren {
			continue
		}
		for g := f.firstChild[c-1]; g != 0; g = f.nextSib[g-1] {
			if f.state[g-1]&foldReceived != 0 {
				dst = append(dst, f.observed(g-1, clk))
			}
		}
	}
	return dst
}

// observed is stub i's interval as of clk, as observedInterval.
func (f *Fold) observed(i uint32, clk *breakdownClock) Interval {
	start := time.Unix(0, f.start[i])
	if f.end[i] == 0 || f.end[i] < f.start[i] {
		clk.live = true
		return Interval{Start: start, End: clk.now}
	}
	return Interval{Start: start, End: time.Unix(0, f.end[i])}
}

// link places i under p. What's folded beneath i, if it arrived before i
// did, now has a retained ancestor.
func (f *Fold) link(i, p uint32) {
	f.parent[i] = p + 1
	f.nextSib[i] = f.firstChild[p]
	f.firstChild[p] = i + 1
	if f.state[p]&foldRetained != 0 && f.state[i]&foldRetained == 0 {
		f.aggOf(p).children++
		f.touch(p)
	}
	if f.state[p]&foldOpenAll != 0 {
		f.state[i] |= foldOpenAll
	}
	if f.state[i]&foldRetained != 0 {
		// retained before it arrived: so are its ancestors (the DB
		// credits them with what's counted on it when it arrives)
		f.promote(p, noSpan)
		return
	}
	if f.firstChild[i] != 0 {
		if a, ok := f.nearestRetained(p); ok {
			sub := f.owned(i)
			f.addOwn(a, sub.foldCounts, 1)
			f.aggOf(a).running += sub.running
		}
	}
}

// foldCounts sums folded spans: their roll-up categories, and how many.
type foldCounts struct {
	RollUpState
	n int32
}

func (c foldCounts) isZero() bool { return c.n == 0 }

func (c *foldCounts) add(d foldCounts, sign int) {
	addRollUp(&c.RollUpState, d.RollUpState, sign)
	c.n += int32(sign) * d.n
}

func oneFolded(cat spanStateCategory) foldCounts {
	var c foldCounts
	c.incrementCategory(cat)
	c.n = 1
	return c
}

// foldedSub sums folded stubs.
type foldedSub struct {
	foldCounts
	// running counts those counted running (foldRunCounted).
	running int32
}

// owned sums the folded stubs beneath i with no retained span between them
// and i: those whose nearest retained ancestor i is, or would be.
func (f *Fold) owned(i uint32) foldedSub {
	var sub foldedSub
	stack := []uint32{f.firstChild[i]}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for ; c != 0; c = f.nextSib[c-1] {
			st := f.state[c-1]
			if st&foldRetained != 0 {
				// what's beneath it is its own
				continue
			}
			if cat := st.cat(); cat != stateUnknown {
				sub.incrementCategory(cat)
				sub.n++
				if st&foldRunCounted != 0 {
					sub.running++
				}
			}
			if f.firstChild[c-1] != 0 {
				stack = append(stack, f.firstChild[c-1])
			}
		}
	}
	return sub
}

// addOwn adds d to (sign > 0) or removes it from what retained span r owns,
// crediting the spans r's counts reach.
func (f *Fold) addOwn(r uint32, d foldCounts, sign int) {
	if d.isZero() {
		return
	}
	f.aggOf(r).own.add(d, sign)
	f.credit(f.spanOf(r), d, sign)
}

// credit adds d to span and every span its roll-up counts reach.
func (f *Fold) credit(span *Span, d foldCounts, sign int) {
	visit := func(s *Span) {
		if s.rollUpState == nil {
			s.rollUpState = &RollUpState{}
		}
		addRollUp(s.rollUpState, d.RollUpState, sign)
		i := f.intern(s.ID)
		f.aggOf(i).beneath += int32(sign) * d.n
		f.touch(i)
	}
	visit(span)
	eachRollUpAncestor(span, visit)
}

// eachRollUpAncestor is rollUpAncestors, without a map for the common case:
// a parent chain with no causes.
func eachRollUpAncestor(span *Span, visit func(*Span)) {
	for s := span; s != nil; s = s.ParentSpan {
		if s.causesViaLinks.Len() > 0 {
			span.rollUpAncestors(nil, visit)
			return
		}
	}
	for s := span.ParentSpan; s != nil; s = s.ParentSpan {
		visit(s)
	}
}

// ownCounts returns the folded counts span owns, for a DB crediting the
// spans that become reachable from it (see Span.recreditLateParent).
func (f *Fold) ownCounts(span *Span) foldCounts {
	if f == nil || len(f.agg) == 0 {
		return foldCounts{}
	}
	i, ok := f.lookup(span.ID)
	if !ok || f.agg[i] == nil {
		return foldCounts{}
	}
	return f.agg[i].own
}

// creditLate adds the folded counts d, owned by a span beneath it, to span,
// which the DB found newly reachable from them (see recreditLateParent).
func (f *Fold) creditLate(span *Span, d foldCounts) {
	if span.rollUpState == nil {
		span.rollUpState = &RollUpState{}
	}
	addRollUp(span.rollUpState, d.RollUpState, 1)
	i := f.intern(span.ID)
	f.aggOf(i).beneath += d.n
	f.touch(i)
}

func addRollUp(st *RollUpState, counts RollUpState, sign int) {
	st.PendingCount += sign * counts.PendingCount
	st.RunningCount += sign * counts.RunningCount
	st.CachedCount += sign * counts.CachedCount
	st.SuccessCount += sign * counts.SuccessCount
	st.FailedCount += sign * counts.FailedCount
	st.CanceledCount += sign * counts.CanceledCount
}

// nearestRetained returns i or its nearest retained ancestor.
func (f *Fold) nearestRetained(i uint32) (uint32, bool) {
	for j := i + 1; j != 0; j = f.parent[j-1] {
		if f.state[j-1]&foldRetained != 0 {
			return j - 1, true
		}
	}
	return 0, false
}

// setCategory moves folded stub i to category c in its nearest retained
// ancestor's aggregates, and so the spans it reaches.
func (f *Fold) setCategory(i uint32, c spanStateCategory) {
	old := f.state[i].cat()
	if old == c {
		return
	}
	f.state[i] = f.state[i]&^foldCatMask | foldState(c)
	if f.parent[i] == 0 {
		return
	}
	r, ok := f.nearestRetained(f.parent[i] - 1)
	if !ok {
		return
	}
	d := oneFolded(c)
	if old != stateUnknown {
		d.decrementCategory(old)
		d.n = 0
	}
	f.aggOf(r).own.add(d, 1)
	f.credit(f.spanOf(r), d, 1)
	agg := f.aggOf(r)
	counted := f.state[i]&foldRunCounted != 0
	switch {
	case counted && c != stateRunning:
		agg.running--
		f.state[i] &^= foldRunCounted
	case !counted && c == stateRunning && f.runningShows(i):
		agg.running++
		f.state[i] |= foldRunCounted
	}
}

// runningShows reports whether folded span i, running, shows its nearest
// retained ancestor's row running: whether the view would place it, as far
// as the stubs tell. A running span the view hides (internal, encapsulated)
// doesn't, as in a full DB (see spanWalker.runningEdges), unless it carries
// progress, which always shows.
func (f *Fold) runningShows(i uint32) bool {
	for j := i + 1; j != 0; j = f.parent[j-1] {
		st := f.state[j-1]
		if st&foldRetained != 0 {
			return true
		}
		if st&foldProgress == 0 && st&(foldInternal|foldEncapsulated) != 0 {
			return false
		}
		if p := f.parent[j-1]; p != 0 && f.state[p-1]&foldEncapsulate != 0 && st&foldProgress == 0 {
			// hidden unless the encapsulating span failed
			if f.state[p-1].cat() != stateFailed {
				return false
			}
		}
	}
	return true
}

// uncredit removes folded stub i from its retained ancestors' aggregates,
// as it's about to be retained: the DB counts it from then on.
func (f *Fold) uncredit(i uint32) {
	old := f.state[i].cat()
	p := f.parent[i]
	if p == 0 {
		return
	}
	if f.state[p-1]&foldRetained != 0 && f.state[i]&foldReceived != 0 {
		f.aggOf(p - 1).children--
	}
	if old == stateUnknown {
		return
	}
	r, ok := f.nearestRetained(p - 1)
	if !ok {
		return
	}
	f.addOwn(r, oneFolded(old), -1)
	if f.state[i]&foldRunCounted != 0 {
		f.aggOf(r).running--
		f.state[i] &^= foldRunCounted
	}
}

// promote retains i and its ancestors not retained yet, reading each folded
// one back except have (whose snapshot the caller holds).
func (f *Fold) promote(i, have uint32) {
	for j := i; ; {
		if f.state[j]&foldRetained != 0 {
			return
		}
		f.uncredit(j)
		// what's folded beneath j moves from its old nearest retained
		// ancestor to j
		sub := f.owned(j)
		if f.parent[j] != 0 {
			if r, ok := f.nearestRetained(f.parent[j] - 1); ok {
				f.addOwn(r, sub.foldCounts, -1)
				f.aggOf(r).running -= sub.running
			}
		}
		f.state[j] |= foldRetained
		span := f.spanOf(j)
		f.addOwn(j, sub.foldCounts, 1)
		agg := f.aggOf(j)
		agg.running += sub.running
		for c := f.firstChild[j]; c != 0; c = f.nextSib[c-1] {
			if f.state[c-1]&(foldRetained|foldReceived) == foldReceived {
				agg.children++
			}
		}
		if f.state[j]&foldHasLogs != 0 {
			span.HasLogs = true
		}
		f.touch(j)
		if j != have && f.state[j]&(foldReceived|foldFull|foldLoading) == foldReceived {
			f.state[j] |= foldLoading
			f.load = append(f.load, f.spans[j])
		}
		if f.parent[j] == 0 {
			return
		}
		j = f.parent[j] - 1
	}
}

// spanOf returns retained span i's Span, a placeholder until it arrives.
func (f *Fold) spanOf(i uint32) *Span {
	return f.db.initSpan(f.spans[i])
}

func (f *Fold) touch(i uint32) {
	if f.touched == nil {
		f.touched = map[uint32]struct{}{}
	}
	f.touched[i] = struct{}{}
}

// expand retains i's children, those folded so far (read back) and those
// still to come.
func (f *Fold) expand(i uint32) {
	f.state[i] |= foldExpanded
	f.promote(i, noSpan)
	for c := f.firstChild[i]; c != 0; c = f.nextSib[c-1] {
		f.promote(c-1, noSpan)
	}
}

// endRoot cancels the folded spans still running, as the DB cancels its
// own when a root completes (see integrateSpan).
func (f *Fold) endRoot(end int64) {
	f.rootEnded = true
	f.rootEnd = end
	for i, st := range f.state {
		if st&foldRetained == 0 && st.cat() == stateRunning {
			f.end[i] = end
			f.setCategory(uint32(i), stateCanceled)
		}
	}
}

// finishBatch brings the retained spans the batch changed up to date, and
// reads back the folded spans it retained.
func (f *Fold) finishBatch() {
	for i := range f.touched {
		span := f.spanOf(i)
		agg := f.agg[i]
		if agg != nil {
			// the expand affordance only asks whether there are any; the
			// children received count as SpanSnapshot does, when it's taken
			if n := span.ChildSpans.Len() + int(agg.children); n > span.ChildCount {
				span.ChildCount = n
			}
		}
		f.db.update(span)
	}
	clear(f.touched)
	if len(f.load) == 0 {
		return
	}
	load := f.load
	f.load = nil
	if f.opts.Load == nil {
		for _, id := range load {
			if i, ok := f.lookup(id); ok {
				f.state[i] &^= foldLoading
			}
		}
		return
	}
	f.Loads += len(load)
	f.opts.Load(load)
}

// Expand retains span id's children, those folded so far (read back) and
// those still to come, as a row expanded shows them. It reports whether any
// is read back.
func (f *Fold) Expand(id SpanID) bool {
	settle := f.db.deferSpanOrder()
	defer settle()
	i := f.intern(id)
	before := f.Loads + len(f.load)
	f.expand(i)
	asked := len(f.load) > 0 && f.Loads+len(f.load) > before
	f.finishBatch()
	return asked
}

// ExpandAll retains everything beneath span id, now and to come.
func (f *Fold) ExpandAll(id SpanID) {
	settle := f.db.deferSpanOrder()
	defer settle()
	i := f.intern(id)
	f.state[i] |= foldOpenAll | foldExpanded
	f.promote(i, noSpan)
	stack := []uint32{i}
	for len(stack) > 0 {
		j := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for c := f.firstChild[j]; c != 0; c = f.nextSib[c-1] {
			f.state[c-1] |= foldOpenAll | foldExpanded
			f.promote(c-1, noSpan)
			stack = append(stack, c-1)
		}
	}
	f.finishBatch()
}

// Retain retains the given spans (and their ancestors), reading back the
// folded ones.
func (f *Fold) Retain(ids ...SpanID) {
	settle := f.db.deferSpanOrder()
	defer settle()
	for _, id := range ids {
		f.promote(f.intern(id), noSpan)
	}
	f.finishBatch()
}

// IsFolded reports whether span id was received and folded.
func (f *Fold) IsFolded(id SpanID) bool {
	if f == nil {
		return false
	}
	i, ok := f.lookup(id)
	return ok && f.state[i]&(foldReceived|foldRetained) == foldReceived
}

// noteHasLogs flags a span as having logs, reporting false when the DB is to
// do it, as it retains the span.
func (f *Fold) noteHasLogs(id SpanID) bool {
	i := f.intern(id)
	if f.state[i]&foldRetained != 0 {
		return false
	}
	f.state[i] |= foldHasLogs
	return true
}

// noteProgress reports whether the DB is to keep progress for span id: it
// retains spans carrying progress that may roll up.
func (f *Fold) noteProgress(id SpanID) bool {
	i := f.intern(id)
	if f.state[i]&foldRetained != 0 {
		return true
	}
	f.state[i] |= foldProgress
	if f.state[i]&foldReceived == 0 || !f.shouldRetain(i) {
		return false
	}
	f.promote(i, noSpan)
	f.finishBatch()
	return true
}

// FoldedBeneath reports whether folded spans sit beneath span, at any depth.
func (f *Fold) FoldedBeneath(span *Span) bool {
	if f == nil || span == nil {
		return false
	}
	i, ok := f.lookup(span.ID)
	if !ok {
		return false
	}
	agg := f.agg[i]
	return agg != nil && agg.beneath > 0
}

// FoldedChildren returns how many of span's children are folded.
func (f *Fold) FoldedChildren(span *Span) int {
	if f == nil || span == nil {
		return 0
	}
	i, ok := f.lookup(span.ID)
	if !ok || f.agg[i] == nil {
		return 0
	}
	return int(f.agg[i].children)
}

// foldedRunning reports whether folded spans run beneath span, with no
// retained span between them.
func (f *Fold) foldedRunning(span *Span) bool {
	if f == nil || len(f.agg) == 0 {
		return false
	}
	i, ok := f.lookup(span.ID)
	if !ok || f.agg[i] == nil {
		return false
	}
	return f.agg[i].running > 0
}

// FoldedPlacements appends the spans whose folded children the view would
// place beneath span's tree: span, and the spans beneath it the view passes
// through to their children (passthrough spans, spans not received), for a
// loader to read back before the view shows span expanded.
func (lv *RowsView) FoldedPlacements(span *Span, dst []SpanID) []SpanID {
	if lv == nil || lv.walker == nil || span.db == nil || span.db.fold == nil {
		return dst
	}
	f := span.db.fold
	stack := []*Span{span}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.FoldedChildren(s) > 0 {
			dst = append(dst, s.ID)
		}
		for _, child := range s.ChildSpans.Spans() {
			if child.ParentSpan == s && lv.walker.mode(child) == modeThrough {
				stack = append(stack, child)
			}
		}
	}
	return dst
}

// FoldStats is a fold's size.
type FoldStats struct {
	// Spans counts the spans received; Retained those retained.
	Spans, Retained int
	// StubBytes is the stub table's own footprint.
	StubBytes int
}

// Stats reports the fold's size.
func (f *Fold) Stats() FoldStats {
	var stats FoldStats
	for _, st := range f.state {
		if st&foldReceived != 0 {
			stats.Spans++
			if st&foldRetained != 0 {
				stats.Retained++
			}
		}
	}
	stats.StubBytes = cap(f.ids.slots)*4 + cap(f.spans)*8 +
		(cap(f.parent)+cap(f.firstChild)+cap(f.nextSib)+cap(f.state))*4 +
		(cap(f.start)+cap(f.end))*8 + cap(f.name)*4
	for _, name := range f.nameTab {
		stats.StubBytes += len(name) + 16 + 32 // the string, its header, its map entry
	}
	return stats
}

// foldIDTable maps span IDs to stub indexes: open addressing over the
// indexes alone (idx+1, 0 empty), comparing against the stubs' own span IDs,
// so a span ID is stored once.
type foldIDTable struct {
	slots []uint32
	n     int
}

func foldHash(id SpanID) uint64 {
	h := binary.LittleEndian.Uint64(id.SpanID[:])
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

func (t *foldIDTable) get(id SpanID, spans []SpanID) (uint32, bool) {
	if len(t.slots) == 0 {
		return 0, false
	}
	mask := uint64(len(t.slots) - 1)
	for h := foldHash(id) & mask; ; h = (h + 1) & mask {
		v := t.slots[h]
		if v == 0 {
			return 0, false
		}
		if spans[v-1] == id {
			return v - 1, true
		}
	}
}

func (t *foldIDTable) put(id SpanID, i uint32, spans []SpanID) {
	if (t.n+1)*4 > len(t.slots)*3 {
		old := t.slots
		t.slots = make([]uint32, max(1024, 2*len(old)))
		t.n = 0
		for _, v := range old {
			if v != 0 {
				t.insert(spans[v-1], v-1)
			}
		}
	}
	t.insert(id, i)
}

func (t *foldIDTable) insert(id SpanID, i uint32) {
	mask := uint64(len(t.slots) - 1)
	h := foldHash(id) & mask
	for t.slots[h] != 0 {
		h = (h + 1) & mask
	}
	t.slots[h] = i + 1
	t.n++
}
