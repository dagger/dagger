package idtui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Span struct {
	sdktrace.ReadOnlySpan

	Digest string

	Call           *callpbv1.Call
	ReceiverDigest string

	Internal bool
	Cached   bool
	Canceled bool
	Inputs   []string

	Primary     bool
	Encapsulate bool
	Passthrough bool
	Ignore      bool

	db    *DB
	trace *Trace
}

func (span *Span) Base() *callpbv1.Call {
	return span.db.HighLevelCall(span.db.MustCall(span.Call.ReceiverDigest))
}

func (span *Span) SimpleReceiver() string {
	return span.db.Simplify(span.ReceiverDigest)
}

func (step *Span) IsRunning() bool {
	return step.EndTime().Before(step.StartTime())
}

func (span *Span) Logs() *Vterm {
	return span.db.Logs[span.SpanContext().SpanID()]
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

func (span *Span) Err() error {
	status := span.Status()
	if status.Code == codes.Error {
		return errors.New(status.Description)
	}
	return nil
}

func (step *Span) IsInternal() bool {
	return step.Internal
}

func (span *Span) Duration() time.Duration {
	var dur time.Duration
	if span.IsRunning() {
		dur = time.Since(span.StartTime())
	} else {
		dur = span.EndTime().Sub(span.StartTime())
	}
	return dur
}

func (step *Span) EndTime() time.Time {
	inner := step.ReadOnlySpan.EndTime()
	if inner.IsZero() {
		return time.Now()
	}
	return inner
}

func (step *Span) IsBefore(other *Span) bool {
	return step.StartTime().Before(other.StartTime())
}

func (span *Span) Children() []*Span {
	children := []*Span{}
	for childID := range span.db.Children[span.SpanContext().SpanID()] {
		child, ok := span.db.Spans[childID]
		if !ok {
			continue
		}
		if !child.Ignore {
			children = append(children, child)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].IsBefore(children[j])
	})
	return children
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
