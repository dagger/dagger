package dagui

import (
	"fmt"
	"strings"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
)

type SpanSet = *OrderedSet[trace.SpanID, *Span]

type Span struct {
	sdktrace.ReadOnlySpan

	ParentSpan *Span
	ChildSpans SpanSet
	LinkedFrom SpanSet
	LinksTo    SpanSet

	ID trace.SpanID

	IsSelfRunning bool
	RunningSpans  SpanSet

	IsSelfFailed bool
	FailedSpans  SpanSet

	Digest string
	Call   *callpbv1.Call
	Base   *callpbv1.Call

	EffectID  string
	EffectIDs []string

	Running  bool
	Internal bool
	Cached   bool
	Canceled bool

	Inputs []string

	Encapsulate  bool
	Encapsulated bool
	Mask         bool
	Passthrough  bool
	Ignore       bool

	db    *DB
	trace *Trace
}

func (span *Span) SetRunning(running bool) {
	span.IsSelfRunning = running
	span.Parents(func(parent *Span) bool { // TODO: go 1.23
		if running {
			parent.RunningSpans.Add(span)
		} else {
			parent.RunningSpans.Remove(span)
		}
		return true
	})
}

func (span *Span) Failed() { // TODO: this can't be undone
	span.IsSelfFailed = true
	if span.EffectID != "" {
		// TODO: do we need to worry about _new_ spans arriving?
		// FIXME: yep - likely when a span starts and finishes in same batch?
		causes := span.db.CauseSpans[span.EffectID]
		if span.EffectID == "sha256:b5b910ed7ac6c90422c4665fba0b50ffa0509d27e273406d6beb7a955e08009f" {
			slog.Warn("recording effect failure", "id", span.EffectID,
				"causes", causes != nil)
		}
		if causes != nil {
			for _, cause := range causes.Order {
				cause.Failed()
			}
		}
	}
	// for parent := range span.Parents {
	// 	if parent.Encapsulate {
	// 		// Reached an encapsulation boundary; stop propagating.
	// 		// TODO: test
	// 		break
	// 	}
	// 	if failed {
	// 		parent.FailedSpans.Add(span)
	// 	} else {
	// 		parent.FailedSpans.Remove(span)
	// 	}
	// 	if parent.Encapsulated {
	// 		// TODO: test
	// 		break
	// 	}
	// }
	for _, parent := range span.LinksTo.Order {
		parent.Failed()
	}
}

func (span *Span) Parents(f func(*Span) bool) {
	var keepGoing bool
	// if the loop breaks while recursing we need to stop recursing, so we track
	// that by man-in-the-middling the return value.
	recurse := func(s *Span) bool {
		keepGoing = f(s)
		return keepGoing
	}
	if span.ParentSpan != nil {
		if !f(span.ParentSpan) {
			return
		}
		span.ParentSpan.Parents(recurse)
		if !keepGoing {
			return
		}
	}
	// for _, parent := range span.LinksTo.Order {
	// 	if !f(parent) {
	// 		return
	// 	}
	// 	parent.Parents(recurse)
	// 	if !keepGoing {
	// 		return
	// 	}
	// }
}

func (span *Span) VisibleParent(opts FrontendOpts) *Span {
	if span.ParentSpan == nil {
		return nil
	}
	// TODO: check links first?
	if span.ParentSpan.Passthrough {
		return span.ParentSpan.VisibleParent(opts)
	}
	links := span.LinksTo.Order
	if len(links) > 0 {
		// prioritize causal spans over the unlazier
		return links[0]
	}
	return span.ParentSpan
}

func (span *Span) Hidden(opts FrontendOpts) bool {
	if span.Ignore {
		// absolutely 100% boring spans, like 'id' and 'sync'
		return true
	}
	if span.IsInternal() && opts.Verbosity < ShowInternalVerbosity {
		// internal spans are hidden by default
		return true
	}
	if span.ParentSpan != nil &&
		(span.Encapsulated || span.ParentSpan.Encapsulate) &&
		!span.ParentSpan.IsFailed() &&
		opts.Verbosity < ShowEncapsulatedVerbosity {
		// encapsulated steps are hidden (even on error) unless their parent errors
		return true
	}
	return false
}

func (span *Span) IsRunning() bool {
	return span.IsSelfRunning || len(span.RunningSpans.Order) > 0
}

func (span *Span) IsPending() bool {
	pending, _ := span.PendingReason()
	return pending
}

func (span *Span) PendingReason() (bool, []string) {
	if span.IsRunning() {
		return false, []string{"span is running"}
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
	if span.Cached {
		return true, []string{"span is cached"}
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

func (span *Span) Name() string {
	return span.ReadOnlySpan.Name()
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

func (span *Span) IsFailed() bool {
	return span.IsSelfFailed || len(span.FailedSpans.Order) > 0
}

func (span *Span) IsInternal() bool {
	return span.Internal
}

type SpanActivity struct {
	Duration time.Duration
	Min      time.Time
	Max      time.Time
}

func (span *Span) SelfDuration(fallbackEnd time.Time) time.Duration {
	if span.IsRunning() {
		return fallbackEnd.Sub(span.StartTime())
	}
	return span.EndTimeOrFallback(fallbackEnd).Sub(span.StartTime())
}

func (span *Span) ActiveDuration(fallbackEnd time.Time) time.Duration {
	facts := SpanActivity{
		Min: span.StartTime(),
		Max: span.EndTimeOrFallback(fallbackEnd),
	}
	facts.Duration = facts.Max.Sub(span.StartTime())

	currentEnd := facts.Max

	for _, effect := range span.LinkedFrom.Order {
		start := effect.StartTime()
		end := effect.EndTimeOrFallback(fallbackEnd)
		duration := end.Sub(start)

		if start.Before(facts.Min) {
			facts.Min = start
		}
		if end.After(facts.Max) {
			facts.Max = end
		}

		if start.Before(currentEnd) {
			// If we started before the last completion, the only case we care about
			// is if we exceed past it.
			if end.After(currentEnd) {
				facts.Duration += end.Sub(currentEnd)
				currentEnd = end
			}
		} else {
			// Started after the last completion, so we just add the duration.
			facts.Duration += duration
			currentEnd = end
		}
	}

	return facts.Duration
}

func (span *Span) EndTimeOrFallback(fallbackEnd time.Time) time.Time {
	if span.IsRunning() {
		return fallbackEnd
	}
	maxTime := span.ReadOnlySpan.EndTime()
	for _, effect := range span.LinkedFrom.Order {
		if effect.EndTime().After(maxTime) {
			maxTime = effect.EndTime()
		}
	}
	return maxTime
}

func (span *Span) EndTime() time.Time {
	return span.EndTimeOrFallback(time.Now())
}

func (span *Span) IsBefore(other *Span) bool {
	return span.StartTime().Before(other.StartTime())
}

type SpanBar struct {
	Span          *Span
	Duration      time.Duration
	OffsetPercent float64
	WidthPercent  float64
}

func (span *Span) Bar() SpanBar {
	epoch := span.trace.Epoch
	end := span.trace.End
	if span.trace.IsRunning {
		end = time.Now()
	}
	total := end.Sub(epoch)

	started := span.StartTime()

	var bar SpanBar
	bar.OffsetPercent = float64(started.Sub(epoch)) / float64(total)
	if span.EndTime().IsZero() {
		bar.WidthPercent = 1 - bar.OffsetPercent
	} else {
		bar.Duration = span.EndTime().Sub(started)
		bar.WidthPercent = float64(bar.Duration) / float64(total)
	}
	bar.Span = span

	return bar
}

func (span *Span) Classes() string {
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
	return strings.Join(classes, " ")
}

func FormatDuration(d time.Duration) string {
	days := int64(d.Hours()) / 24
	hours := int64(d.Hours()) % 24
	minutes := int64(d.Minutes()) % 60
	seconds := d.Seconds() - float64(86400*days) - float64(3600*hours) - float64(60*minutes)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm%.1fs", minutes, seconds)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm%.1fs", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%dd%dh%dm%.1fs", days, hours, minutes, seconds)
	}
}
