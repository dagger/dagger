package dagui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

type Span struct {
	sdktrace.ReadOnlySpan

	ParentSpan *Span
	ChildSpans []*Span

	ID trace.SpanID

	IsSelfRunning bool

	Digest string
	Call   *callpbv1.Call
	Base   *callpbv1.Call

	EffectIDs []string

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

func (span *Span) LinkedFrom() []*Span {
	var linked []*Span
	for linkerID := range span.db.LinkedFrom[span.ID] {
		linker, ok := span.db.Spans[linkerID]
		if ok {
			linked = append(linked, linker)
		}
	}
	return linked
}

func (span *Span) IsRunning() bool {
	if span.IsSelfRunning {
		return true
	}
	for _, src := range span.LinkedFrom() {
		if src.IsRunning() {
			return true
		}
	}
	return false
}

func (span *Span) ChildrenAndLinkedSpans() []*Span {
	linkers := span.LinkedFrom()
	if len(linkers) == 0 {
		return span.ChildSpans
	}
	res := append([]*Span{}, span.ChildSpans...)
	for _, s := range linkers {
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].StartTime().Before(res[j].StartTime())
	})
	return res
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
			// if the LLB span has arrived, so we're no longer pending
			for _, effect := range span.LinkedFrom() {
				if effect.Digest == digest {
					return false, []string{
						"effect " + digest + " has started",
					}
				}
			}
			reasons = append(reasons, "effect "+digest+" has not started")
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

// TODO: fix this up
func (span *Span) CachedReason() (bool, []string) {
	if span.Cached {
		return true, []string{"span is cached"}
	}
	states := map[bool]int{}
	reasons := []string{}
	track := func(effect string, cached bool) {
		states[cached]++
		if cached {
			reasons = append(reasons, fmt.Sprintf("effect %s is cached", effect))
		} else {
			reasons = append(reasons, fmt.Sprintf("effect %s is NOT cached", effect))
		}
	}
	for _, effect := range span.EffectIDs {
		// first check for spans we've seen for the effect
		effectSpans := span.db.EffectSpans[effect]
		if len(effectSpans) > 0 {
			for _, span := range effectSpans {
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
	if span.Status().Code == codes.Error {
		return true
	}
	for _, effect := range span.LinkedFrom() {
		if effect.IsFailed() {
			return true
		}
	}
	return false
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

	for _, effect := range span.LinkedFrom() {
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
	return span.ReadOnlySpan.EndTime()
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
