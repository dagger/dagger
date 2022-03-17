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

// This file contains logic for running tasks.
//
// This implementation anticipates that workflows can also be used for defining
// servers, not just batch scripts. In the future, tasks may be long running and
// provide streams of results.
//
// The implementation starts a goroutine for each user-defined task, instead of
// having a fixed pool of workers. The main reason for this is that tasks are
// inherently heterogeneous and may be blocking on top of that. Also, in the
// future tasks may be long running, as discussed above.

import (
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/internal/core/adt"
	"cuelang.org/go/internal/core/eval"
	"cuelang.org/go/internal/value"
)

func (c *Controller) runLoop() {
	_, root := value.ToInternal(c.inst)

	// Copy the initial conjuncts.
	n := len(root.Conjuncts)
	c.conjuncts = make([]adt.Conjunct, n, n+len(c.tasks))
	copy(c.conjuncts, root.Conjuncts)

	c.markReady(nil)

	for c.errs == nil {
		// Dispatch all unblocked tasks to workers. Only update
		// the configuration when all have been dispatched.

		waiting := false
		running := false

		// Mark tasks as Ready.
		for _, t := range c.tasks {
			switch t.state {
			case Waiting:
				waiting = true

			case Ready:
				running = true

				t.state = Running
				c.updateTaskValue(t)

				t.ctxt = eval.NewContext(value.ToInternal(t.v))

				go func(t *Task) {
					if err := t.r.Run(t, nil); err != nil {
						t.err = errors.Promote(err, "task failed")
					}

					t.c.taskCh <- t
				}(t)

			case Running:
				running = true

			case Terminated:
			}
		}

		if !running {
			if waiting {
				// Should not happen ever, as cycle detection should have caught
				// this. But keep this around as a defensive measure.
				c.addErr(errors.New("deadlock"), "run loop")
			}
			break
		}

		select {
		case <-c.context.Done():
			return

		case t := <-c.taskCh:
			t.state = Terminated

			switch t.err {
			case nil:
				c.updateTaskResults(t)

			case ErrAbort:
				// TODO: do something cleverer.
				fallthrough

			default:
				c.addErr(t.err, "task failure")
				return
			}

			// Recompute the configuration, if necessary.
			if c.updateValue() {
				// initTasks was already called in New to catch initialization
				// errors earlier.
				c.initTasks()
			}

			c.updateTaskValue(t)

			c.markReady(t)
		}
	}
}

func (c *Controller) markReady(t *Task) {
	for _, x := range c.tasks {
		if x.state == Waiting && x.isReady() {
			x.state = Ready
		}
	}

	if c.cfg.UpdateFunc != nil {
		if err := c.cfg.UpdateFunc(c, t); err != nil {
			c.addErr(err, "task completed")
			c.cancel()
			return
		}
	}
}

// updateValue recomputes the workflow configuration if it is out of date. It
// reports whether the values were updated.
func (c *Controller) updateValue() bool {

	if c.valueSeqNum == c.conjunctSeq {
		return false
	}

	// TODO: incrementally update results. Currently, the entire tree is
	// recomputed on every update. This should not be necessary with the right
	// notification structure in place.

	v := &adt.Vertex{Conjuncts: c.conjuncts}
	v.Finalize(c.opCtx)

	c.inst = value.Make(c.opCtx, v)
	c.valueSeqNum = c.conjunctSeq
	return true
}

// updateTaskValue updates the value of the task in the configuration if it is
// out of date.
func (c *Controller) updateTaskValue(t *Task) {
	required := t.conjunctSeq
	for _, dep := range t.depTasks {
		if dep.conjunctSeq > required {
			required = dep.conjunctSeq
		}
	}

	if t.valueSeq == required {
		return
	}

	if c.valueSeqNum < required {
		c.updateValue()
	}

	t.v = c.inst.LookupPath(t.path)
	t.valueSeq = required
}

// updateTaskResults updates the result status of the task and adds any result
// values to the overall configuration.
func (c *Controller) updateTaskResults(t *Task) bool {
	if t.update == nil {
		return false
	}

	expr := t.update
	for i := len(t.labels) - 1; i >= 0; i-- {
		expr = &adt.StructLit{
			Decls: []adt.Decl{
				&adt.Field{
					Label: t.labels[i],
					Value: expr,
				},
			},
		}
	}

	t.update = nil

	// TODO: replace rather than add conjunct if this task already added a
	// conjunct before. This will allow for serving applications.
	c.conjuncts = append(c.conjuncts, adt.MakeRootConjunct(c.env, expr))
	c.conjunctSeq++
	t.conjunctSeq = c.conjunctSeq

	return true
}
