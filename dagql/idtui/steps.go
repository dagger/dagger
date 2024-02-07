package idtui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/vito/progrock"
)

type Step struct {
	Base   *Step
	Digest string

	db *DB
}

func (step *Step) ID() *idproto.ID {
	// TODO cache?
	return step.db.IDs[step.Digest]
}

func (step *Step) HasStarted() bool {
	return len(step.db.Intervals[step.Digest]) > 0
}

func (step *Step) FirstVertex() bool {
	ivals := step.db.Intervals[step.Digest]
	if len(ivals) == 0 {
		return false
	}
	for _, vtx := range ivals {
		if vtx.Completed == nil {
			return true
		}
	}
	return false
}

func (step *Step) IsRunning() bool {
	ivals := step.db.Intervals[step.Digest]
	if len(ivals) == 0 {
		return false
	}
	for _, vtx := range ivals {
		if vtx.Completed == nil {
			return true
		}
	}
	return false
}

func (step *Step) Name() string {
	for _, vtx := range step.db.Intervals[step.Digest] {
		return vtx.Name // assume all names are equal
	}
	if step.ID() != nil {
		return step.ID().DisplaySelf()
	}
	return "<unnamed>"
}

func (step *Step) Inputs() []string {
	for _, vtx := range step.db.Intervals[step.Digest] {
		return vtx.Inputs // assume all names are equal
	}
	if step.ID() != nil {
		// TODO: in principle this could return arg ID digests, but not needed
		return nil
	}
	return nil
}

func (step *Step) Err() error {
	for _, vtx := range step.db.Intervals[step.Digest] {
		if vtx.Error != nil {
			return errors.New(vtx.GetError())
		}
	}
	return nil
}

func (step *Step) IsInternal() bool {
	for _, vtx := range step.db.Intervals[step.Digest] {
		if vtx.Internal {
			return true
		}
	}
	return false
}

func (step *Step) Duration() time.Duration {
	var d time.Duration
	for _, vtx := range step.db.Intervals[step.Digest] {
		d += vtx.Duration()
	}
	return d
}

func (step *Step) FirstCompleted() *time.Time {
	var completed *time.Time
	for _, vtx := range step.db.Intervals[step.Digest] {
		if vtx.Completed == nil {
			continue
		}
		cmp := vtx.Completed.AsTime()
		if completed == nil {
			completed = &cmp
			continue
		}
		if cmp.Before(*completed) {
			completed = &cmp
		}
	}
	return completed
}

func (step *Step) StartTime() time.Time {
	ivals := step.db.Intervals[step.Digest]
	if len(ivals) == 0 {
		return time.Time{}
	}
	lowest := time.Now()
	for started := range ivals {
		if started.Before(lowest) {
			lowest = started
		}
	}
	return lowest
}

func (step *Step) EndTime() time.Time {
	now := time.Now()
	ivals := step.db.Intervals[step.Digest]
	if len(ivals) == 0 {
		return now
	}
	var highest time.Time
	for _, vtx := range ivals {
		if vtx.Completed == nil {
			highest = now
		} else if vtx.Completed.AsTime().After(highest) {
			highest = vtx.Completed.AsTime()
		}
	}
	return highest
}

func (step *Step) IsBefore(other *Step) bool {
	as, bs := step.StartTime(), other.StartTime()
	switch {
	case as.Before(bs):
		return true
	case bs.Before(as):
		return false
	case step.EndTime().Before(other.EndTime()):
		return true
	case other.EndTime().Before(step.EndTime()):
		return false
	default:
		// equal start + end time; maybe a cache hit. break ties by seeing if one
		// depends on the other.
		return step.db.IsTransitiveDependency(other.Digest, step.Digest)
	}
}

func (step *Step) Children() []*Step {
	children := []*Step{}
	for out := range step.db.Children[step.Digest] {
		child, ok := step.db.Step(out)
		if !ok {
			continue
		}
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].IsBefore(children[j])
	})
	return children
}

type Span struct {
	Duration      time.Duration
	OffsetPercent float64
	WidthPercent  float64
	Vertex        *progrock.Vertex
}

func (step *Step) Spans() (spans []Span) {
	epoch := step.db.Epoch
	end := step.db.End

	ivals := step.db.Intervals[step.Digest]
	if len(ivals) == 0 {
		return
	}

	total := end.Sub(epoch)

	for started, vtx := range ivals {
		var span Span
		span.OffsetPercent = float64(started.Sub(epoch)) / float64(total)
		if vtx.Completed != nil {
			span.Duration = vtx.Completed.AsTime().Sub(started)
			span.WidthPercent = float64(span.Duration) / float64(total)
		} else {
			span.WidthPercent = 1 - span.OffsetPercent
		}
		span.Vertex = vtx
		spans = append(spans, span)
	}

	sort.Slice(spans, func(i, j int) bool {
		return spans[i].OffsetPercent < spans[j].OffsetPercent
	})

	return
}

func (span Span) Bar() templ.Component {
	var dur string
	if span.Duration > 10*time.Millisecond {
		dur = fmtDuration(span.Duration)
	}
	return templ.Raw(
		fmt.Sprintf(
			`<div class="bar %s" style="left: %f%%; width: %f%%"><span class="duration">%s</span></div>`,
			VertexClasses(span.Vertex),
			span.OffsetPercent*100,
			span.WidthPercent*100,
			dur,
		),
	)
}

func VertexClasses(vtx *progrock.Vertex) string {
	classes := []string{}
	if vtx.Cached {
		classes = append(classes, "cached")
	}
	if vtx.Canceled {
		classes = append(classes, "canceled")
	}
	if vtx.Error != nil {
		classes = append(classes, "errored")
	}
	if vtx.Focused {
		classes = append(classes, "focused")
	}
	if vtx.Internal {
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
