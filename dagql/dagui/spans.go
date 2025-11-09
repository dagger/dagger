package dagui

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
)

type SpanSet = *OrderedSet[SpanID, *Span]

// RollUpState caches the computed state for rendering RollUp progress bars
type RollUpState struct {
	PendingCount  int
	RunningCount  int
	CachedCount   int
	SuccessCount  int
	FailedCount   int
	CanceledCount int
}

type Span struct {
	SpanSnapshot

	ParentSpan    *Span   `json:"-"`
	ChildSpans    SpanSet `json:"-"`
	RunningSpans  SpanSet `json:"-"`
	FailedLinks   SpanSet `json:"-"`
	CanceledLinks SpanSet `json:"-"`
	RevealedSpans SpanSet `json:"-"`

	ErrorOrigin *Span `json:"-"`

	callCache *callpbv1.Call
	baseCache *callpbv1.Call

	// v0.15+
	causesViaLinks  SpanSet
	effectsViaLinks SpanSet
	// v0.14 and below
	causesViaAttrs  SpanSet
	effectsViaAttrs map[string]SpanSet

	// Indicates that this span was actually exported to the database, and not
	// just allocated due to a span parent or other relationship.
	Received bool

	// Pre-computed RollUp state for rendering progress bars (only for RollUp spans)
	rollUpState *RollUpState

	db *DB
}

// Snapshot returns a snapshot of the span's current state.
func (span *Span) Snapshot() SpanSnapshot {
	span.ChildCount = countChildren(span.ChildSpans, FrontendOpts{})
	span.Failed_, span.FailedReason_ = span.FailedReason()
	span.Cached_, span.CachedReason_ = span.CachedReason()
	span.Pending_, span.PendingReason_ = span.PendingReason()
	span.Canceled_, span.CanceledReason_ = span.CanceledReason()
	snapshot := span.SpanSnapshot
	snapshot.Final = true // NOTE: applied to copy
	return snapshot
}

func (span *Span) Call() *callpbv1.Call {
	if span.callCache != nil {
		return span.callCache
	}
	if span.CallDigest == "" {
		return nil
	}
	span.callCache = span.db.Call(span.CallDigest)
	return span.callCache
}

func (span *Span) CallID() (*call.ID, error) {
	spanCall := span.Call()
	if spanCall == nil {
		return nil, fmt.Errorf("no call for span")
	}

	dag := &callpbv1.DAG{
		RootDigest:    spanCall.Digest,
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	extractIntoDAG(dag, span.db, spanCall.Digest)

	var id call.ID
	err := id.FromProto(dag)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (span *Span) Base() *callpbv1.Call {
	if span.baseCache != nil {
		return span.baseCache
	}

	call := span.Call()
	if call == nil {
		return nil
	}

	// TODO: respect an already-set base value computed server-side, and client
	// subsequently requests necessary DAG
	if call.ReceiverDigest != "" {
		parentCall := span.db.MustCall(call.ReceiverDigest)
		if parentCall != nil {
			span.baseCache = span.db.Simplify(parentCall, span.Internal)
			return span.baseCache
		}
	}

	return nil
}

func countChildren(set SpanSet, opts FrontendOpts) int {
	count := 0
	for _, child := range set.Order {
		if child.Passthrough && !opts.Debug {
			count += countChildren(child.ChildSpans, opts)
		} else {
			count += 1
		}
	}
	return count
}

type SpanSnapshot struct {
	// Monotonically increasing number for each update seen for this span.
	Version int

	// Indicates that this snapshot is in its final state and should be trusted
	// over any state derived from the local state.
	// This is used for snapshots that come from a remote server.
	Final bool

	ID        SpanID
	Name      string
	StartTime time.Time
	EndTime   time.Time

	Activity Activity `json:",omitzero"`

	ParentID SpanID     `json:",omitzero"`
	Links    []SpanLink `json:",omitempty"`

	Status sdktrace.Status `json:",omitzero"`

	// statuses derived from span and its effects
	Failed_         bool     `json:",omitempty"`
	FailedReason_   []string `json:",omitempty"`
	Cached_         bool     `json:",omitempty"`
	CachedReason_   []string `json:",omitempty"`
	Pending_        bool     `json:",omitempty"`
	PendingReason_  []string `json:",omitempty"`
	Canceled_       bool     `json:",omitempty"`
	CanceledReason_ []string `json:",omitempty"`

	// statuses reported by the span via attributes
	Canceled bool `json:",omitempty"`
	Cached   bool `json:",omitempty"`

	// UI preferences reported by the span, or applied to it (sync=>passthrough)
	Internal     bool `json:",omitempty"`
	Encapsulate  bool `json:",omitempty"`
	Encapsulated bool `json:",omitempty"`
	Passthrough  bool `json:",omitempty"`
	Ignore       bool `json:",omitempty"`
	Reveal       bool `json:",omitempty"`
	RollUp       bool `json:",omitempty"`

	// Check name + status
	CheckName   string `json:",omitempty"`
	CheckPassed bool   `json:",omitempty"`

	ActorEmoji  string `json:",omitempty"`
	Message     string `json:",omitempty"`
	ContentType string `json:",omitempty"`

	LLMRole          string   `json:",omitempty"`
	LLMTool          string   `json:",omitempty"`
	LLMToolServer    string   `json:",omitempty"`
	LLMToolArgNames  []string `json:",omitempty"`
	LLMToolArgValues []string `json:",omitempty"`

	Inputs []string `json:",omitempty"`
	Output string   `json:",omitempty"`

	EffectID         string   `json:",omitempty"`
	EffectIDs        []string `json:",omitempty"`
	EffectsCompleted []string `json:",omitempty"`

	CallDigest  string `json:",omitempty"`
	CallPayload string `json:",omitempty"`
	CallScope   string `json:",omitempty"`

	ChildCount int  `json:",omitempty"`
	HasLogs    bool `json:",omitempty"`

	ExtraAttributes map[string]json.RawMessage `json:",omitempty"`
}

type SpanLink struct {
	SpanContext SpanContext
	Purpose     string
}

func (snapshot *SpanSnapshot) ProcessAttribute(name string, val any) {
	defer func() {
		// a bit of a shortcut, but there shouldn't be much going on
		// here and all the conversion error handling code is
		// annoying
		if err := recover(); err != nil {
			slog.Warn("panic processing attribute", "name", name, "val", val, "err", err)
		}
	}()

	switch name {
	case telemetry.DagDigestAttr:
		snapshot.CallDigest = val.(string)

	case telemetry.DagCallAttr:
		snapshot.CallPayload = val.(string)

	case telemetry.DagCallScopeAttr:
		snapshot.CallScope = val.(string)

	case telemetry.CachedAttr:
		snapshot.Cached = val.(bool)

	case telemetry.CanceledAttr:
		snapshot.Canceled = val.(bool)

	case telemetry.UIEncapsulateAttr:
		snapshot.Encapsulate = val.(bool)

	case telemetry.UIEncapsulatedAttr:
		snapshot.Encapsulated = val.(bool)

	case telemetry.UIRevealAttr:
		snapshot.Reveal = val.(bool)

	case telemetry.UIInternalAttr:
		snapshot.Internal = val.(bool)

	case telemetry.UIPassthroughAttr:
		snapshot.Passthrough = val.(bool)

	case telemetry.UIActorEmojiAttr:
		snapshot.ActorEmoji = val.(string)

	case telemetry.UIMessageAttr:
		snapshot.Message = val.(string)

	case telemetry.UIRollupAttr:
		snapshot.RollUp = val.(bool)

	case telemetry.CheckNameAttr:
		snapshot.CheckName = val.(string)

	case telemetry.CheckPassedAttr:
		// TODO: redundant with span status?
		snapshot.CheckPassed = val.(bool)

	case telemetry.LLMRoleAttr:
		snapshot.LLMRole = val.(string)

	case telemetry.LLMToolAttr:
		snapshot.LLMTool = val.(string)

	case telemetry.LLMToolServerAttr:
		snapshot.LLMToolServer = val.(string)

	case telemetry.LLMToolArgNamesAttr:
		snapshot.LLMToolArgNames = sliceOf[string](val)

	case telemetry.LLMToolArgValuesAttr:
		snapshot.LLMToolArgValues = sliceOf[string](val)

	case telemetry.DagInputsAttr:
		snapshot.Inputs = sliceOf[string](val)

	case telemetry.EffectIDsAttr:
		snapshot.EffectIDs = sliceOf[string](val)

	case telemetry.EffectsCompletedAttr:
		snapshot.EffectsCompleted = sliceOf[string](val)

	case telemetry.DagOutputAttr:
		snapshot.Output = val.(string)

	case telemetry.EffectIDAttr:
		snapshot.EffectID = val.(string)

	case telemetry.ContentTypeAttr:
		snapshot.ContentType = val.(string)

	case "rpc.service":
		// encapsulate these by default; we only maybe want to see these if their
		// parent failed, since some happy paths might involve _expected_ failures
		snapshot.Encapsulated = true

	default:
		if snapshot.ExtraAttributes == nil {
			snapshot.ExtraAttributes = make(map[string]json.RawMessage)
		}
		payload, err := json.Marshal(val)
		if err != nil {
			slog.Warn("failed to marshal attribute", "attribute", name, "val", val)
			return
		}
		snapshot.ExtraAttributes[name] = json.RawMessage(payload)
	}
}

func sliceOf[T any](val any) []T {
	if direct, ok := val.([]T); ok {
		return direct
	}
	slice := val.([]any)
	ts := make([]T, len(slice))
	for i, v := range slice {
		ts[i] = v.(T)
	}
	return ts
}

// PropagateStatusToParentsAndLinks updates the running and failed state of all
// parent spans, linked spans, and their parents to reflect the span.
//
// NOTE: failed state only propagates to spans that installed the current
// span's effect - it does _not_ propagate through the parent span.
func (span *Span) PropagateStatusToParentsAndLinks() {
	propagate := func(parent *Span, causal, activity bool) bool {
		var changed bool
		if span.IsRunningOrEffectsRunning() {
			changed = parent.RunningSpans.Add(span)
		} else {
			changed = parent.RunningSpans.Remove(span)
		}
		if span.Reveal || parent.ErrorOrigin == span {
			changed = parent.RevealedSpans.Add(span) || changed
		}
		if causal && span.IsFailed() {
			changed = parent.FailedLinks.Add(span) || changed
		}
		if causal && span.IsCanceled() {
			changed = parent.CanceledLinks.Add(span) || changed
		}
		if activity && parent.Activity.Add(span) {
			changed = true
		}
		return changed
	}

	for parent := range span.Parents {
		// don't propagate failure, to respect encapsulation
		// don't propagate activity, since these are direct parents
		if propagate(parent, false, false) {
			span.db.update(parent)
		}
	}

	for causal := range span.CausalSpans {
		// propagate activity and failure, since causal spans inherit both
		if propagate(causal, true, true) {
			span.db.update(causal)
		}

		for parent := range causal.Parents {
			// don't propagate failure, to respect encapsulation
			// do propagate activity, since these are indirect parents
			if propagate(parent, false, true) {
				span.db.update(parent)
			}
		}
	}

	// Update RollUp state for any RollUp ancestors
	span.updateRollUpAncestors()
}

// updateRollUpAncestors recomputes RollUp state for all RollUp ancestors
func (span *Span) updateRollUpAncestors() {
	// Use a map to track which ancestors we've already updated to avoid redundant work
	updated := make(map[SpanID]bool)

	// Use a queue to iteratively process ancestors instead of recursion
	var queue []*Span

	// Add immediate parents to queue
	if span.ParentSpan != nil {
		queue = append(queue, span.ParentSpan)
	}

	// Add causal parents to queue
	span.CausalSpans(func(causal *Span) bool {
		queue = append(queue, causal)
		return true
	})

	// Process ancestors iteratively
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Skip if already updated
		if updated[current.ID] {
			continue
		}
		updated[current.ID] = true

		// Update this ancestor if it's a RollUp span
		if current.RollUp {
			current.computeRollUpState()
		}

		// Add this ancestor's parents to queue
		if current.ParentSpan != nil {
			queue = append(queue, current.ParentSpan)
		}

		// Add this ancestor's causal parents to queue
		current.CausalSpans(func(causal *Span) bool {
			queue = append(queue, causal)
			return true
		})
	}
}

// computeRollUpState pre-computes the state for rendering RollUp progress bars
// This is called when children change, not on every render frame.
func (span *Span) computeRollUpState() {
	if !span.RollUp {
		return
	}

	// Collect all descendant spans (recursively)
	children := span.collectAllDescendants()
	if len(children) == 0 {
		span.rollUpState = nil
		return
	}

	state := &RollUpState{}

	for _, child := range children {
		if child.IsRunningOrEffectsRunning() {
			state.RunningCount++
		} else if child.IsPending() {
			state.PendingCount++
		} else if child.IsCached() {
			state.CachedCount++
		} else if child.IsCanceled() {
			state.CanceledCount++
		} else if child.IsFailedOrCausedFailure() {
			state.FailedCount++
		} else {
			// Success (completed but not cached, canceled, or failed)
			state.SuccessCount++
		}
	}

	span.rollUpState = state
}

// collectAllDescendants recursively collects all descendant spans
func (span *Span) collectAllDescendants() []*Span {
	var children []*Span
	seen := make(map[SpanID]bool)

	var collect func(*Span)
	collect = func(s *Span) {
		// Use ChildSpans directly since we don't have opts here
		for _, child := range s.ChildSpans.Order {
			if seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			children = append(children, child)
			// Recursively collect grandchildren
			collect(child)
		}
	}

	collect(span)
	return children
}

// RollUpState returns the pre-computed RollUp state for rendering progress bars
func (span *Span) RollUpState() *RollUpState {
	return span.rollUpState
}

func (span *Span) ChildOrRevealedSpans(opts FrontendOpts) (SpanSet, bool) {
	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[span.ID]; ok {
		verbosity = v
	}
	if len(span.RevealedSpans.Order) > 0 && !opts.RevealNoisySpans && verbosity < ShowSpammyVerbosity {
		return span.RevealedSpans, true
	} else {
		return span.ChildSpans, false
	}
}

func (span *Span) IsOK() bool {
	return span.Status.Code == codes.Ok
}

func (span *Span) IsFailed() bool {
	return span.Status.Code == codes.Error
}

func (span *Span) IsUnset() bool {
	return span.Status.Code == codes.Unset
}

// Errors returns the individual errored spans contributing to the span's
// Failed or CausedFailure status.
func (span *Span) Errors() SpanSet {
	errs := NewSpanSet()
	if span.IsFailed() {
		errs.Add(span)
	}
	if len(errs.Order) > 0 {
		return errs
	}
	for _, failed := range span.FailedLinks.Order {
		errs.Add(failed)
	}
	if len(errs.Order) > 0 {
		return errs
	}
	for _, effect := range span.EffectIDs {
		if span.db.FailedEffects[effect] {
			if effectSpans := span.db.EffectSpans[effect]; effectSpans != nil {
				for _, e := range effectSpans.Order {
					if e.IsFailed() {
						errs.Add(e)
					}
				}
			}
		}
	}
	return errs
}

func (span *Span) IsFailedOrCausedFailure() bool {
	if span.Final {
		return span.Failed_
	}
	if span.IsFailed() || len(span.FailedLinks.Order) > 0 {
		return true
	}
	for _, effect := range span.EffectIDs {
		if span.db.FailedEffects[effect] {
			return true
		}
	}
	return false
}

func (span *Span) FailedReason() (bool, []string) {
	if span.Final {
		return span.Failed_, span.FailedReason_
	}
	var reasons []string
	if span.IsFailed() {
		reasons = append(reasons, "span itself errored")
	}
	for _, failed := range span.FailedLinks.Order {
		reasons = append(reasons, "span has failed link: "+failed.Name)
	}
	for _, effect := range span.EffectIDs {
		if span.db.FailedEffects[effect] {
			reasons = append(reasons, "span installed failed effect: "+effect)
		}
	}
	return len(reasons) > 0, reasons
}

func (span *Span) IsCanceled() bool {
	canceled, _ := span.CanceledReason()
	return canceled
}

func (span *Span) CanceledReason() (bool, []string) {
	if span.Final {
		return span.Canceled_, span.CanceledReason_
	}
	var reasons []string
	if span.Canceled {
		reasons = append(reasons, "span says it is canceled")
	}
	for _, canceled := range span.CanceledLinks.Order {
		reasons = append(reasons, "span has canceled link: "+canceled.Name)
	}
	return len(reasons) > 0, reasons
}

func (span *Span) Parents(f func(*Span) bool) {
	if span.ParentSpan != nil {
		if !f(span.ParentSpan) {
			return
		}
		for parent := range span.ParentSpan.Parents {
			if !f(parent) {
				break
			}
		}
	}
}

func (span *Span) Hidden(opts FrontendOpts) bool {
	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[span.ID]; ok {
		verbosity = v
	}
	if span.IsInternal() && verbosity < ShowInternalVerbosity {
		// internal spans are hidden by default
		return true
	}
	if span.ParentSpan != nil &&
		(span.Encapsulated || span.ParentSpan.Encapsulate) &&
		!span.ParentSpan.IsFailed() &&
		verbosity < ShowEncapsulatedVerbosity {
		// encapsulated steps are hidden (even on error) unless their parent errors
		return true
	}
	return false
}

func (span *Span) IsRunning() bool {
	return span.EndTime.Before(span.StartTime)
}

// CausalSpans iterates over the spans that directly cause this span, by following
// links (for newer engines) or attributes (for old engines).
func (span *Span) CausalSpans(f func(*Span) bool) {
	var visit func(*Span) bool
	visit = func(s *Span) bool {
		if !f(s) {
			return false
		}
		for cause := range s.CausalSpans {
			if !visit(cause) {
				return false
			}
		}
		return true
	}
	for _, cause := range span.causesViaLinks.Order {
		if !visit(cause) {
			return
		}
	}
	if span.causesViaAttrs != nil {
		for _, cause := range span.causesViaAttrs.Order {
			if span.StartTime.Before(cause.StartTime) {
				// cannot possibly be "caused" by it, since it came after
				continue
			}
			if !visit(cause) {
				return
			}
		}
	}
}

func (span *Span) EffectSpans(f func(*Span) bool) {
	if len(span.effectsViaLinks.Order) > 0 {
		for _, span := range span.effectsViaLinks.Order {
			if !f(span) {
				return
			}
		}
		return
	}
	for _, set := range span.effectsViaAttrs {
		for _, span := range set.Order {
			if !f(span) {
				return
			}
		}
	}
}

func (span *Span) IsRunningOrEffectsRunning() bool {
	return span.IsRunning() ||
		// leverage the work that goes into span.Activity, rather than having two
		// sources of truth
		span.Activity.IsRunning()
}

func (span *Span) IsPending() bool {
	pending, _ := span.PendingReason()
	return pending
}

func (span *Span) PendingReason() (bool, []string) {
	if span.Final {
		return span.Pending_, span.PendingReason_
	}
	if span.IsRunningOrEffectsRunning() {
		var reasons []string
		if span.IsRunning() {
			reasons = append(reasons, "span is running")
		}
		for _, running := range span.RunningSpans.Order {
			reasons = append(reasons, "span has running link: "+running.Name)
		}
		return false, reasons
	}
	var reasons []string
	if len(span.EffectIDs) > 0 {
		for _, digest := range span.EffectIDs {
			effectSpans := span.db.EffectSpans[digest]
			if effectSpans != nil && len(effectSpans.Order) > 0 {
				return false, []string{
					digest + " has started",
				}
			}
			if span.db.CompletedEffects[digest] {
				return false, []string{
					digest + " has completed",
				}
			}
			reasons = append(reasons, digest+" has not started")
		}
		// there's an output but no linked spans yet, so we're pending
		return true, reasons
	}
	return false, []string{"span has completed"}
}

func (span *Span) IsCached() bool {
	cached, _ := span.CachedReason()
	return cached
}

func (span *Span) CachedReason() (bool, []string) {
	if span.Final {
		return span.Cached_, span.CachedReason_
	}
	if span.Cached {
		return true, []string{"span says it is cached"}
	}
	if span.ChildCount > 0 {
		return false, []string{"span has children"}
	}
	if span.HasLogs {
		return false, []string{"span has logs"}
	}
	states := map[bool]int{}
	reasons := []string{}
	track := func(effect string, cached bool) {
		states[cached]++
		if cached {
			reasons = append(reasons, fmt.Sprintf("%s is cached", effect))
		} else {
			reasons = append(reasons, fmt.Sprintf("%s is not cached", effect))
		}
	}
	for _, effect := range span.EffectIDs {
		// first check for spans we've seen for the effect
		effectSpans := span.db.EffectSpans[effect]
		if effectSpans != nil && len(effectSpans.Order) > 0 {
			for _, span := range effectSpans.Order {
				track(effect, span.IsCached())
			}
		} else {
			// if the effect is completed but we never saw a span for it, that
			// might mean it was a multiple-layers-deep cache hit. or, some
			// buildkit bug caused us to never see the span. or, another parallel
			// client completed it. in all of those cases, we'll at least consider
			// it cached so it's not stuck 'pending' forever.
			track(effect, span.db.CompletedEffects[effect])
		}
	}
	if len(states) == 1 && states[true] > 0 {
		// all effects were cached
		return true, reasons
	}
	// some effects were not cached
	return false, reasons
}

func (span *Span) HasParent(parent *Span) bool {
	if span.ParentSpan == nil {
		return false
	}
	if span.ParentSpan == parent {
		return true
	}
	return span.ParentSpan.HasParent(parent)
}

// func (step *Step) Inputs() []string {
// 	for _, vtx := range step.db.Intervals[step.Digest] {
// 		return vtx.Inputs // assume all names are equal
// 	}
// 	if step.ID() != nil {
// 		// TODO: in principle this could return arg ID digests, but not needed
// 		return nil
// 	}
// 	return nil
// }

func (span *Span) IsInternal() bool {
	return span.Internal
}

func (span *Span) EndTimeOrFallback(fallbackEnd time.Time) time.Time {
	return span.Activity.EndTimeOrFallback(fallbackEnd)
}

func (span *Span) EndTimeOrNow() time.Time {
	return span.EndTimeOrFallback(time.Now())
}

func (span *Span) Before(other *Span) bool {
	return span.StartTime.Before(other.StartTime)
}

func (span *Span) Classes() []string {
	classes := []string{}
	if span.Cached {
		classes = append(classes, "cached")
	}
	if span.Canceled {
		classes = append(classes, "canceled")
	}
	if span.IsFailed() {
		classes = append(classes, "errored")
	}
	if span.Internal {
		classes = append(classes, "internal")
	}
	return classes
}

func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "INVALID_DURATION"
	}

	days := int64(d.Hours()) / 24
	hours := int64(d.Hours()) % 24
	minutes := int64(d.Minutes()) % 60
	seconds := d.Seconds() - float64(86400*days) - float64(3600*hours) - float64(60*minutes)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", minutes, int(math.Round(seconds)))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, int(math.Round(seconds)))
	default:
		return fmt.Sprintf("%dd%dh%dm%ds", days, hours, minutes, int(math.Round(seconds)))
	}
}
