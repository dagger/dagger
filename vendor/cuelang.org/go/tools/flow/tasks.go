// Copyright 2020 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package flow

// This file contains functionality for identifying tasks in the configuration
// and annotating the dependencies between them.

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/internal/core/adt"
	"cuelang.org/go/internal/core/dep"
	"cuelang.org/go/internal/value"
)

// initTasks takes the current configuration and adds tasks to the list of
// tasks. It can be run multiple times on increasingly more concrete
// configurations to add more tasks, whereby the task pointers of previously
// found tasks are preserved.
func (c *Controller) initTasks() {
	// Clear previous cache.
	c.nodes = map[*adt.Vertex]*Task{}

	v := c.inst.LookupPath(c.cfg.Root)
	if err := v.Err(); err != nil {
		c.addErr(err, "invalid root")
		c.cancel()
		return
	}

	// Mark any task that is located under the root.
	c.findRootTasks(v)

	// Mark any tasks that are implied by dependencies.
	// Note that the list of tasks may grow as this loop progresses.
	for i := 0; i < len(c.tasks); i++ {
		t := c.tasks[i]
		c.markTaskDependencies(t, t.vertex())
	}

	// Check if there are cycles in the task dependencies.
	if err := checkCycle(c.tasks); err != nil {
		c.addErr(err, "cyclic task")
	}

	if c.errs != nil {
		c.cancel()
	}
}

// findRootTasks finds tasks under the root.
func (c *Controller) findRootTasks(v cue.Value) {
	t := c.getTask(nil, v)

	if t != nil {
		return
	}

	opts := []cue.Option{}

	if c.cfg.FindHiddenTasks {
		opts = append(opts, cue.Hidden(true), cue.Definitions(false))
	}

	for iter, _ := v.Fields(opts...); iter.Next(); {
		c.findRootTasks(iter.Value())
	}
}

// This file contains the functionality to locate and record the tasks of
// a configuration. It:
//   - create Task struct for each node that is a task
//   - associate nodes in a configuration with a Task, if applicable.
// The node-to-task map is used to determine task dependencies.

// getTask finds and marks tasks that are descendents of v.
func (c *Controller) getTask(scope *Task, v cue.Value) *Task {
	// Look up cached node.
	_, w := value.ToInternal(v)
	if t, ok := c.nodes[w]; ok {
		return t
	}

	// Look up cached task from previous evaluation.
	p := v.Path()
	key := p.String()

	t := c.keys[key]

	if t == nil {
		r, err := c.isTask(v)

		var errs errors.Error
		if err != nil {
			if !c.inRoot(w) {
				// Must be in InferTask mode. In this case we ignore the error.
				r = nil
			} else {
				c.addErr(err, "invalid task")
				errs = errors.Promote(err, "create task")
			}
		}

		if r != nil {
			index := len(c.tasks)
			t = &Task{
				v:      v,
				c:      c,
				r:      r,
				path:   p,
				labels: w.Path(),
				key:    key,
				index:  index,
				err:    errs,
			}
			c.tasks = append(c.tasks, t)
			c.keys[key] = t
		}
	}

	// Process nodes of task for this evaluation.
	if t != nil {
		scope = t
		if t.state <= Ready {
			// Don't set the value if the task is currently running as this may
			// result in all kinds of inconsistency issues.
			t.v = v
		}

		c.tagChildren(w, t)
	}

	c.nodes[w] = scope

	return t
}

func (c *Controller) tagChildren(n *adt.Vertex, t *Task) {
	for _, a := range n.Arcs {
		c.nodes[a] = t
		c.tagChildren(a, t)
	}
}

// findImpliedTask determines the task of corresponding to node n, if any. If n
// is not already associated with a task, it tries to determine whether n is
// part of a task by checking if any of the parent nodes is a task.
//
// TODO: it is actually more accurate to check for tasks from top down. TODO:
// What should be done if a subtasks is referenced that is embedded in another
// task. Should the surrounding tasks be added as well?
func (c *Controller) findImpliedTask(d dep.Dependency) *Task {
	// Ignore references into packages. Fill will fundamentally not work for
	// packages, and packages cannot point back to the main package as cycles
	// are not allowed.
	if d.Import() != nil {
		return nil
	}

	n := d.Node

	// This Finalize should not be necessary, as the input to dep is already
	// finalized. However, cue cmd uses some legacy instance stitching code
	// where some of the backlink Environments are not properly initialized.
	// Finalizing should patch those up at the expense of doing some duplicate
	// work. The plan is to replace `cue cmd` with a much more clean
	// implementation (probably a separate tool called `cuerun`) where this
	// issue is fixed. For now we leave this patch.
	//
	// Note that this issue predates package flow, but that it just surfaced in
	// flow and having a different evaluation order.
	//
	// Note: this call is cheap if n is already Finalized.
	n.Finalize(c.opCtx)

	for ; n != nil; n = n.Parent {
		if c.cfg.IgnoreConcrete && n.IsConcrete() {
			if k := n.BaseValue.Kind(); k != adt.StructKind && k != adt.ListKind {
				return nil
			}
		}

		t, ok := c.nodes[n]
		if ok || !c.cfg.InferTasks {
			return t
		}

		if !d.IsRoot() {
			v := value.Make(c.opCtx, n)

			if t := c.getTask(nil, v); t != nil {
				return t
			}
		}
	}

	return nil
}

// markTaskDependencies traces through all conjuncts of a Task and marks
// any dependencies on other tasks.
//
// The dependencies for a node by traversing the nodes of a task and then
// traversing the dependencies of the conjuncts.
//
// This terminates because:
//
//  - traversing all nodes of all tasks is guaranteed finite (CUE does not
//    evaluate to infinite structure).
//
//  - traversing conjuncts of all nodes is finite, as the input syntax is
//    inherently finite.
//
//  - as regular nodes are traversed recursively they are marked with a cycle
//    marker to detect cycles, ensuring a finite traversal as well.
//
func (c *Controller) markTaskDependencies(t *Task, n *adt.Vertex) {
	dep.VisitFields(c.opCtx, n, func(d dep.Dependency) error {
		depTask := c.findImpliedTask(d)
		if depTask != nil {
			if depTask != cycleMarker {
				v := value.Make(c.opCtx, d.Node)
				t.addDep(v.Path().String(), depTask)
			}
			return nil
		}

		// If this points to a non-task node, it may itself point to a task.
		// Handling this allows for dynamic references. For instance, such a
		// value may reference the result value of a task, or even create
		// new tasks based on the result of another task.
		if d.Import() == nil {
			c.nodes[d.Node] = cycleMarker
			c.markTaskDependencies(t, d.Node)
			c.nodes[d.Node] = nil
		}
		return nil
	})
}

func (c *Controller) inRoot(n *adt.Vertex) bool {
	path := value.Make(c.opCtx, n).Path().Selectors()
	root := c.cfg.Root.Selectors()
	if len(path) < len(root) {
		return false
	}
	for i, sel := range root {
		if path[i] != sel {
			return false
		}
	}
	return true
}

var cycleMarker = &Task{}
