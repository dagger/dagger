package idtui

import (
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

func (step *Step) FirstVertex() *progrock.Vertex {
	// TODO cache?
	return step.db.FirstVertex(step.Digest)
}

func (s *Step) IsRunning() bool {
	ivals := s.db.Intervals[s.Digest]
	if ivals == nil || len(ivals) == 0 {
		return false
	}
	for _, vtx := range ivals {
		if vtx.Completed == nil {
			return true
		}
	}
	return false
}

func (s *Step) StartTime() time.Time {
	ivals := s.db.Intervals[s.Digest]
	if ivals == nil || len(ivals) == 0 {
		return time.Time{}
	}
	var lowest time.Time = time.Now()
	for started := range ivals {
		if started.Before(lowest) {
			lowest = started
		}
	}
	return lowest
}

func (s *Step) EndTime() time.Time {
	now := time.Now()
	ivals := s.db.Intervals[s.Digest]
	if ivals == nil || len(ivals) == 0 {
		return now
	}
	var highest time.Time = time.Time{}
	for _, vtx := range ivals {
		if vtx.Completed == nil {
			highest = now
		} else if vtx.Completed.AsTime().After(highest) {
			highest = vtx.Completed.AsTime()
		}
	}
	return highest
}

func (s *Step) IsBefore(other *Step) bool {
	as, bs := s.StartTime(), other.StartTime()
	switch {
	case as.Before(bs):
		return true
	case bs.Before(as):
		return false
	case s.EndTime().Before(other.EndTime()):
		return true
	case other.EndTime().Before(s.EndTime()):
		return false
	default:
		// equal start + end time; maybe a cache hit. break ties by seeing if one
		// depends on the other.
		return s.db.IsTransitiveDependency(other.Digest, s.Digest)
	}
}

func (s *Step) Children() []*Step {
	children := []*Step{}
	for out := range s.db.Children[s.Digest] {
		child, ok := s.db.Step(out)
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
	if ivals == nil {
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
