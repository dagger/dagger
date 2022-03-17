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

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// checkCycle checks for cyclic dependencies between tasks.
func checkCycle(a []*Task) errors.Error {
	cc := cycleChecker{
		visited: make([]bool, len(a)),
		stack:   make([]*Task, 0, len(a)),
	}
	for _, t := range a {
		if cc.isCyclic(t) {
			break
		}
	}
	return cc.err
}

type cycleChecker struct {
	visited []bool
	stack   []*Task
	err     errors.Error
}

func (cc *cycleChecker) isCyclic(t *Task) bool {
	i := t.index
	if !cc.visited[i] {
		cc.visited[i] = true
		cc.stack = append(cc.stack, t)

		for _, d := range t.depTasks {
			if !cc.visited[d.index] && cc.isCyclic(d) {
				return true
			} else if cc.visited[d.index] {
				cc.addCycleError(t)
				return true
			}
		}
	}
	cc.stack = cc.stack[:len(cc.stack)-1]
	cc.visited[i] = false
	return false
}

func (cc *cycleChecker) addCycleError(start *Task) {
	err := &cycleError{}

	for _, t := range cc.stack {
		err.path = append(err.path, t.v.Path())
		err.positions = append(err.positions, t.v.Pos())
	}

	cc.err = errors.Append(cc.err, err)
}

type cycleError struct {
	path      []cue.Path
	positions []token.Pos
}

func (e *cycleError) Error() string {
	msg, args := e.Msg()
	return fmt.Sprintf(msg, args...)
}

func (e *cycleError) Path() []string { return nil }

func (e *cycleError) Msg() (format string, args []interface{}) {
	w := &strings.Builder{}
	for _, p := range e.path {
		fmt.Fprintf(w, "\n\ttask %s refers to", p)
	}
	fmt.Fprintf(w, "\n\ttask %s", e.path[0])

	return "cyclic task dependency:%v", []interface{}{w.String()}
}

func (e *cycleError) Position() token.Pos {
	if len(e.positions) == 0 {
		return token.NoPos
	}
	return e.positions[0]
}

func (e *cycleError) InputPositions() []token.Pos {
	return e.positions
}
