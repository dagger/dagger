package idtui

import (
	"sort"
	"time"
)

func CollectSteps(db *DB) []*Step {
	var steps []*Step // nolint:prealloc
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

type Pipeline []TraceRow

type TraceRow struct {
	Step *Step

	Parent *TraceRow

	IsRunning bool
	ByParent  bool

	Children []*TraceRow
}

func (row *TraceRow) IsInteresting() bool {
	return row.IsRunning ||
		row.Step.FirstVertex().Duration() > 100*time.Millisecond ||
		row.Step.FirstVertex().Error != nil
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
		if step.Base != nil {
			row.ByParent = step.Base.Digest == lastSeen
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
