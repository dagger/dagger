package idtui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a-h/templ"
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

	Internal bool
	Cached   bool
	Canceled bool
	EffectID string

	Inputs         []string
	Effects        []string
	RunningEffects map[string]*Span
	FailedEffects  map[string]*Span

	Encapsulate  bool
	Encapsulated bool
	Mask         bool
	Passthrough  bool
	Ignore       bool

	db    *DB
	trace *Trace
}

func (span *Span) IsRunning() bool {
	return span.IsSelfRunning || len(span.RunningEffects) > 0
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

func (span *Span) ChildrenAndEffects() []*Span {
	var children []*Span
	children = append(children, span.ChildSpans...)
	children = append(children, span.EffectSpans()...)
	return children
}

func (span *Span) EffectSpans() []*Span {
	var effects []*Span
	for _, e := range span.Effects {
		if s, ok := span.db.Effects[e]; ok {
			effects = append(effects, s)
		}
	}
	return effects
}

func (span *Span) Failed() bool {
	return span.Status().Code == codes.Error ||
		len(span.FailedEffects) > 0
}

func (span *Span) Err() error {
	status := span.Status()
	if status.Code == codes.Error {
		return errors.New(status.Description)
	}
	return nil
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

	for _, effect := range span.EffectSpans() {
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

func (bar SpanBar) Render() templ.Component {
	var dur string
	if bar.Duration > 10*time.Millisecond {
		dur = fmtDuration(bar.Duration)
	}
	return templ.Raw(
		fmt.Sprintf(
			`<div class="bar %s" style="left: %f%%; width: %f%%"><span class="duration">%s</span></div>`,
			bar.Span.Classes(),
			bar.OffsetPercent*100,
			bar.WidthPercent*100,
			dur,
		),
	)
}

func (span *Span) Classes() string {
	classes := []string{}
	if span.Cached {
		classes = append(classes, "cached")
	}
	if span.Canceled {
		classes = append(classes, "canceled")
	}
	if span.Err() != nil {
		classes = append(classes, "errored")
	}
	if span.Internal {
		classes = append(classes, "internal")
	}
	return strings.Join(classes, " ")
}

func fmtDuration(d time.Duration) string {
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
