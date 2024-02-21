package idtui

import (
	"sort"
	"time"
)

func CollectSteps(db *DB) []*Step {
	var steps []*Step //nolint:prealloc
	for vID := range db.Vertices {
		step, ok := db.Step(vID)
		if !ok {
			continue
		}
		steps = append(steps, step)
	}
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].IsBefore(steps[j])
	})
	return steps
}

func CollectRows(steps []*Step) []*TraceRow {
	var rows []*TraceRow
	WalkSteps(steps, func(row *TraceRow) {
		if row.Parent != nil {
			row.Parent.Children = append(row.Parent.Children, row)
		} else {
			rows = append(rows, row)
		}
	})
	return rows
}

type TraceRow struct {
	Step *Step

	Parent *TraceRow

	IsRunning bool
	Chained   bool

	Children  []*TraceRow
	Collapsed bool
}

type Pipeline []*TraceRow

func CollectPipelines(rows []*TraceRow) []Pipeline {
	pls := []Pipeline{}
	var cur Pipeline
	for _, r := range rows {
		switch {
		case len(cur) == 0:
			cur = append(cur, r)
		case r.Chained:
			cur = append(cur, r)
		case len(cur) > 0:
			pls = append(pls, cur)
			cur = Pipeline{r}
		}
	}
	if len(cur) > 0 {
		pls = append(pls, cur)
	}
	return pls
}

type LogsView struct {
	Primary *Step
	Body    []*TraceRow
	Init    *TraceRow
}

func CollectLogsView(rows []*TraceRow) *LogsView {
	view := &LogsView{}
	for _, row := range rows {
		switch {
		case view.Primary == nil && row.Step.Digest == PrimaryVertex:
			// promote children of primary vertex to the top-level
			for _, child := range row.Children {
				child.Parent = nil
			}
			view.Primary = row.Step
			view.Body = row.Children
		case view.Primary == nil && row.Step.Digest == InitVertex:
			// collapse initialization steps by default
			//
			// TODO: I added this and immediately disliked it, but let's give it time
			// and see what others think naturally
			row.Collapsed = true
			view.Init = row
		default:
			// reveal anything 'extra' by default (fail open)
			view.Body = append(view.Body, row)
		}
	}
	return view
}

const (
	TooFastThreshold = 100 * time.Millisecond
	GCThreshold      = 1 * time.Second
)

func (row *TraceRow) IsInteresting() bool {
	step := row.Step
	if step.Err() != nil {
		// show errors always
		return true
	}
	if step.IsInternal() {
		// internal steps are, by definition, not interesting
		return false
	}
	if step.Duration() < TooFastThreshold {
		// ignore fast steps; signal:noise is too poor
		return false
	}
	if row.IsRunning {
		// show things once they've been running for a while
		return true
	}
	if completed := step.FirstCompleted(); completed != nil && time.Since(*completed) < GCThreshold {
		// show things that just completed, to reduce flicker
		return true
	}
	return false
}

func (row *TraceRow) Depth() int {
	if row.Parent == nil {
		return 0
	}
	return row.Parent.Depth() + 1
}

func (row *TraceRow) setRunning() {
	row.IsRunning = true
	if row.Parent != nil && !row.Parent.IsRunning {
		row.Parent.setRunning()
	}
}

func WalkSteps(steps []*Step, f func(*TraceRow)) {
	var lastSeen string
	seen := map[string]bool{}
	var walk func(*Step, *TraceRow)
	walk = func(step *Step, parent *TraceRow) {
		if seen[step.Digest] {
			return
		}
		row := &TraceRow{
			Step:   step,
			Parent: parent,
		}
		if step.BaseDigest != "" {
			row.Chained = step.BaseDigest == lastSeen
		}
		if step.IsRunning() {
			row.setRunning()
		}
		f(row)
		lastSeen = step.Digest
		seen[step.Digest] = true
		for _, child := range step.Children() {
			walk(child, row)
		}
		lastSeen = step.Digest
	}
	for _, step := range steps {
		walk(step, nil)
	}
}
