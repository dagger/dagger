package dagui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
)

type SpanSet = *OrderedSet[SpanID, *Span]

// spanStateCategory represents the primary state of a span for rollup counting
type spanStateCategory uint8

const (
	stateUnknown spanStateCategory = iota
	stateRunning
	statePending
	stateCached
	stateCanceled
	stateFailed
	stateSuccess
)

// RollUpState caches the computed state for rendering RollUp progress bars
type RollUpState struct {
	PendingCount  int
	RunningCount  int
	CachedCount   int
	SuccessCount  int
	FailedCount   int
	CanceledCount int
}

func (st *RollUpState) Reset() {
	st.PendingCount = 0
	st.RunningCount = 0
	st.CachedCount = 0
	st.SuccessCount = 0
	st.FailedCount = 0
	st.CanceledCount = 0
}

// incrementCategory adds 1 to the count for the given category
func (st *RollUpState) incrementCategory(cat spanStateCategory) {
	switch cat {
	case stateRunning:
		st.RunningCount++
	case statePending:
		st.PendingCount++
	case stateCached:
		st.CachedCount++
	case stateCanceled:
		st.CanceledCount++
	case stateFailed:
		st.FailedCount++
	case stateSuccess:
		st.SuccessCount++
	}
}

// decrementCategory subtracts 1 from the count for the given category
func (st *RollUpState) decrementCategory(cat spanStateCategory) {
	switch cat {
	case stateRunning:
		st.RunningCount--
	case statePending:
		st.PendingCount--
	case stateCached:
		st.CachedCount--
	case stateCanceled:
		st.CanceledCount--
	case stateFailed:
		st.FailedCount--
	case stateSuccess:
		st.SuccessCount--
	}
}

type Span struct {
	SpanSnapshot

	ParentSpan    *Span   `json:"-"`
	ChildSpans    SpanSet `json:"-"`
	RunningSpans  SpanSet `json:"-"`
	FailedLinks   SpanSet `json:"-"`
	CanceledLinks SpanSet `json:"-"`
	RevealedSpans SpanSet `json:"-"`
	ErrorOrigins  SpanSet `json:"-"`

	callCache *callpbv1.Call
	baseCache *callpbv1.Call

	causesViaLinks  SpanSet
	effectsViaLinks SpanSet

	// Indicates that this span was actually exported to the database, and not
	// just allocated due to a span parent or other relationship.
	Received bool

	// Pre-computed RollUp state for rendering progress bars
	// Maintained incrementally for all spans, not just those marked RollUp
	rollUpState *RollUpState

	// Cached state classification for incremental updates
	lastRollUpCategory spanStateCategory

	db *DB
}

// Snapshot returns a snapshot of the span's current state.
func (span *Span) Snapshot() SpanSnapshot {
	// Never decrease this value; it may have been calculated from a SQL query,
	// indicating that the span has children but we didn't fetch them
	// (incremental loading).
	span.ChildCount = max(span.ChildCount, countChildren(span.ChildSpans, FrontendOpts{}))
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

	recipe := &callpbv1.RecipeDAG{
		RootDigest:    spanCall.Digest,
		CallsByDigest: map[string]*callpbv1.Call{},
	}
	extractIntoDAG(recipe, span.db, spanCall.Digest)
	dag := &callpbv1.DAG{
		Value: &callpbv1.DAG_Recipe{
			Recipe: recipe,
		},
	}

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
		parentCall := span.db.Call(call.ReceiverDigest)
		if parentCall == nil {
			suffix := "." + call.Field
			if span.Name != "" && strings.HasSuffix(span.Name, suffix) {
				baseName := strings.TrimSuffix(span.Name, suffix)
				if baseName != "" {
					span.baseCache = &callpbv1.Call{
						Digest: call.ReceiverDigest,
						Type: &callpbv1.Type{
							NamedType: baseName,
						},
					}
					return span.baseCache
				}
			}
		}
		parentCall = span.db.MustCall(call.ReceiverDigest)
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
	TraceID   TraceID
	Name      string
	StartTime time.Time
	EndTime   time.Time

	Activity Activity `json:",omitzero"`

	ParentID SpanID     `json:",omitzero"`
	Links    []SpanLink `json:",omitempty"`

	Status sdktrace.Status `json:",omitzero"`

	// statuses derived from the span and any causal continuations
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
	Pending  bool `json:",omitempty"`

	// An extra flag to indicate that a span was canceled because the root span
	// completed while the span was still running.
	LeftRunning bool `json:",omitempty"`

	// UI preferences reported by the span, or applied to it (sync=>passthrough)
	Internal     bool `json:",omitempty"`
	Encapsulate  bool `json:",omitempty"`
	Encapsulated bool `json:",omitempty"`
	Passthrough  bool `json:",omitempty"`
	Ignore       bool `json:",omitempty"`

	// Test attributes
	TestCaseName  string     `json:",omitempty"`
	TestSuiteName string     `json:",omitempty"`
	TestStatus    TestStatus `json:",omitempty"`

	Boundary    bool `json:",omitempty"`
	Reveal      bool `json:",omitempty"`
	RollUpLogs  bool `json:",omitempty"`
	RollUpSpans bool `json:",omitempty"`

	// Check name + status
	CheckName   string `json:",omitempty"`
	CheckPassed bool   `json:",omitempty"`

	// Generator name
	GeneratorName string `json:",omitempty"`

	// Service name
	ServiceName string `json:",omitempty"`

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

	ResumeOutput string `json:",omitempty"`

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

func (snapshot *SpanSnapshot) ProcessAttribute(name string, val any) { //nolint: gocyclo
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

	case telemetry.PendingAttr:
		snapshot.Pending = val.(bool)

	case telemetry.UIEncapsulateAttr:
		snapshot.Encapsulate = val.(bool)

	case telemetry.UIEncapsulatedAttr:
		snapshot.Encapsulated = val.(bool)

	case telemetry.UIRevealAttr:
		snapshot.Reveal = val.(bool)

	case telemetry.UIBoundaryAttr:
		snapshot.Boundary = val.(bool)

	case telemetry.UIInternalAttr:
		snapshot.Internal = val.(bool)

	case telemetry.UIPassthroughAttr:
		snapshot.Passthrough = val.(bool)

	case telemetry.UIActorEmojiAttr:
		snapshot.ActorEmoji = val.(string)

	case telemetry.UIMessageAttr:
		snapshot.Message = val.(string)

	case telemetry.UIRollUpLogsAttr:
		snapshot.RollUpLogs = val.(bool)

	case telemetry.UIRollUpSpansAttr:
		snapshot.RollUpSpans = val.(bool)

	case telemetry.CheckNameAttr:
		snapshot.CheckName = val.(string)

	case telemetry.CheckPassedAttr:
		// TODO: redundant with span status?
		snapshot.CheckPassed = val.(bool)

	case telemetry.GeneratorNameAttr:
		snapshot.GeneratorName = val.(string)

	case "dagger.io/service.name":
		snapshot.ServiceName = val.(string)

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

	case telemetry.DagOutputAttr:
		snapshot.Output = val.(string)

	case telemetryattrs.UIResumeOutputAttr:
		snapshot.ResumeOutput = val.(string)

	case telemetry.ContentTypeAttr:
		snapshot.ContentType = val.(string)

	case string(semconv.TestCaseNameKey):
		snapshot.TestCaseName = val.(string)

	case string(semconv.TestSuiteNameKey):
		snapshot.TestSuiteName = val.(string)

	case string(semconv.TestSuiteRunStatusKey), string(semconv.TestCaseResultStatusKey):
		snapshot.TestStatus = mergeTestStatus(snapshot.TestStatus, normalizeTestStatus(val.(string)))

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
// NOTE: failed state propagates through causal links, not through the direct
// parent span.
func (span *Span) PropagateStatusToParentsAndLinks() {
	// Update the span's own activity to reflect its current state
	span.Activity.Add(span)

	propagate := func(parent *Span, causal, activity bool) bool {
		var changed bool
		if span.IsRunningOrEffectsRunning() {
			changed = parent.RunningSpans.Add(span)
		} else {
			changed = parent.RunningSpans.Remove(span)
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
		changed := propagate(parent, false, false)

		// If a child only starts after its parent already completed, treat it as
		// a resumed continuation of that parent for failure purposes.
		lateContinuation := !parent.IsRunning() && span.StartTime.After(parent.EndTime)
		if lateContinuation && span.IsFailedOrCausedFailure() {
			changed = parent.FailedLinks.Add(span) || changed
		}

		if changed {
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

	// Handle revealed spans propagation separately to stop at revealed parents
	if span.Reveal {
		for parent := range span.Parents {
			if parent.RevealedSpans.Add(span) {
				span.db.update(parent)
			}

			if parent.Boundary || parent.Encapsulate || parent.Reveal {
				break
			}
		}
	}

	// Update RollUp state for ancestors incrementally
	span.updateRollUpAncestors()

	if span.db != nil {
		span.db.noteTestSpanUpdated(span)
	}
}

// currentStateCategory determines the span's current state category for rollup counting
func (span *Span) currentStateCategory() spanStateCategory {
	if span.IsRunningOrEffectsRunning() {
		return stateRunning
	} else if span.IsPending() {
		return statePending
	} else if span.IsCached() {
		return stateCached
	} else if span.IsCanceled() {
		return stateCanceled
	} else if span.IsFailedOrCausedFailure() {
		return stateFailed
	} else {
		// Success (completed but not cached, canceled, or failed)
		return stateSuccess
	}
}

// updateRollUpAncestors incrementally updates RollUp state for all ancestors
// This is O(depth) instead of O(descendants) per update
func (span *Span) updateRollUpAncestors() {
	// Determine the span's current state category
	newCategory := span.currentStateCategory()
	oldCategory := span.lastRollUpCategory

	// If the category hasn't changed, no update needed
	if newCategory == oldCategory {
		return
	}

	// Update the cached category
	span.lastRollUpCategory = newCategory

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

		// Lazily initialize rollUpState for all spans
		if current.rollUpState == nil {
			current.rollUpState = &RollUpState{}
		}

		// Incrementally update this ancestor's counts
		if oldCategory != stateUnknown {
			current.rollUpState.decrementCategory(oldCategory)
		}
		current.rollUpState.incrementCategory(newCategory)

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

// initializeRollUpState computes the initial RollUp state by traversing descendants once
// This can be used to rebuild state from scratch if needed (e.g., for debugging or recovery).
// Under normal operation, the incremental updates in updateRollUpAncestors are sufficient.
//
//nolint:unused
func (span *Span) initializeRollUpState() {
	if span.rollUpState == nil {
		span.rollUpState = &RollUpState{}
	} else {
		span.rollUpState.Reset()
	}

	// Recursively count all descendants
	for child := range span.Descendants {
		category := child.currentStateCategory()
		span.rollUpState.incrementCategory(category)
		// Also initialize the child's cached category for future incremental updates
		child.lastRollUpCategory = category
	}
}

// Descendants recursively iterates through all descendant spans
func (span *Span) Descendants(f func(*Span) bool) {
	var collect func(*Span) bool
	collect = func(s *Span) bool {
		// Use ChildSpans directly since we don't have opts here
		for _, child := range s.ChildSpans.Order {
			if !f(child) {
				return false
			}
			// Recursively collect grandchildren
			if !collect(child) {
				return false
			}
		}
		return true
	}

	collect(span)
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
	return errs
}

func (span *Span) IsFailedOrCausedFailure() bool {
	return span.IsFailed() || (span.FailedLinks != nil && len(span.FailedLinks.Order) > 0) || (span.Final && span.Failed_)
}

func (span *Span) FailedReason() (bool, []string) {
	var reasons []string
	if span.IsFailed() {
		reasons = append(reasons, "span itself errored")
	}
	for _, failed := range span.FailedLinks.Order {
		reasons = append(reasons, "span has failed link: "+failed.Name)
	}
	if len(reasons) == 0 && span.Final && span.Failed_ {
		reasons = append(reasons, span.FailedReason_...)
	}
	return len(reasons) > 0, reasons
}

func (span *Span) IsCanceled() bool {
	return span.Canceled || len(span.CanceledLinks.Order) > 0
}

func (span *Span) CanceledReason() (bool, []string) {
	if span.Final {
		return span.Canceled_, span.CanceledReason_
	}
	var reasons []string
	if span.LeftRunning {
		reasons = append(reasons, "span was left running after the root span completed")
	} else if span.Canceled {
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

// CausalSpans iterates over the spans that directly cause this span.
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
}

func (span *Span) EffectSpans(f func(*Span) bool) {
	for _, span := range span.effectsViaLinks.Order {
		if !f(span) {
			return
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
	// NB: keep this in extremely close alignment with PendingReason, we don't
	// re-use it so we can minimize allocations

	if span.IsRunningOrEffectsRunning() {
		return false
	}
	if span.Pending || (span.Final && span.Pending_) {
		return len(span.effectsViaLinks.Order) == 0
	}
	return false
}

func (span *Span) PendingReason() (bool, []string) {
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
	if span.Pending || (span.Final && span.Pending_) {
		if len(span.effectsViaLinks.Order) > 0 {
			return false, []string{"span has resumed via causal continuation"}
		}
		return true, []string{"span says it is pending"}
	}
	return false, []string{"span has completed"}
}

func (span *Span) IsCached() bool {
	// NB: keep this in extremely close alignment with CachedReason, we don't
	// re-use it so we can minimize allocations

	return span.Cached || (span.Final && span.Cached_)
}

func (span *Span) CachedReason() (bool, []string) {
	if span.Cached {
		return true, []string{"span says it is cached"}
	}
	if span.Final && span.Cached_ {
		return true, span.CachedReason_
	}
	return false, []string{"span is not cached"}
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
