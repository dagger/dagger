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

// Package flow provides a low-level workflow manager based on a CUE Instance.
//
// A Task defines an operational unit in a Workflow and corresponds to a struct
// in a CUE instance. This package does not define what a Task looks like in a
// CUE Instance. Instead, the user of this package must supply a TaskFunc that
// creates a Runner for cue.Values that are deemed to be a Task.
//
// Tasks may depend on other tasks. Cyclic dependencies are thereby not allowed.
// A Task A depends on another Task B if A, directly or indirectly, has a
// reference to any field of Task B, including its root.
package flow

// TODO: Add hooks. This would allow UIs, for instance, to report on progress.
//
// - New(inst *cue.Instance, options ...Option)
// - AddTask(v cue.Value, r Runner) *Task
// - AddDependency(a, b *Task)
// - AddTaskGraph(root cue.Value, fn taskFunc)
// - AddSequence(list cue.Value, fn taskFunc)
// - Err()

// TODO:
// Should we allow lists as a shorthand for a sequence of tasks?
// If so, how do we specify termination behavior?

// TODO:
// Should we allow tasks to be a child of another task? Currently, the search
// for tasks end once a task root is found.
//
// Semantically it is somewhat unclear to do so: for instance, if an $after
// is used to refer to an explicit task dependency, it is logically
// indistinguishable whether this should be a subtask or is a dependency.
// Using higher-order constructs for analysis is generally undesirable.
//
// A possible solution would be to define specific "grouping tasks" whose sole
// purpose is to define sub tasks. The user of this package would then need
// to explicitly distinguish between tasks that are dependencies and tasks that
// are subtasks.

// TODO: streaming tasks/ server applications
//
// Workflows are currently implemented for batch processing, for instance to
// implement shell scripting or other kinds of batch processing.
//
// This API has been designed, however, to also allow for streaming
// applications. For instance, a streaming Task could listen for Etcd changes
// or incoming HTTP requests and send updates each time an input changes.
// Downstream tasks could then alternate between a Waiting and Running state.
//
// Note that such streaming applications would also cause configurations to
// potentially not become increasingly more specific. Instead, a Task would
// replace its old result each time it is updated. This would require tracking
// of which conjunct was previously created by a task.

import (
	"context"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/internal/core/adt"
	"cuelang.org/go/internal/core/convert"
	"cuelang.org/go/internal/core/eval"
	"cuelang.org/go/internal/value"
)

var (
	// ErrAbort may be returned by a task to avoid processing downstream tasks.
	// This can be used by control nodes to influence execution.
	ErrAbort = errors.New("abort dependant tasks without failure")

	// TODO: ErrUpdate: update and run a dependency, but don't complete a
	// dependency as more results may come. This is useful in server mode.
)

// A TaskFunc creates a Runner for v if v defines a task or reports nil
// otherwise. It reports an error for illformed tasks.
//
// If TaskFunc returns a non-nil Runner the search for task within v stops.
// That is, subtasks are not supported.
type TaskFunc func(v cue.Value) (Runner, error)

// A Runner executes a Task.
type Runner interface {
	// Run runs a Task. If any of the tasks it depends on returned an error it
	// is passed to this task. It reports an error upon failure.
	//
	// Any results to be returned can be set by calling Fill on the passed task.
	//
	// TODO: what is a good contract for receiving and passing errors and abort.
	//
	// If for a returned error x errors.Is(x, ErrAbort), all dependant tasks
	// will not be run, without this being an error.
	Run(t *Task, err error) error
}

// A RunnerFunc runs a Task.
type RunnerFunc func(t *Task) error

func (f RunnerFunc) Run(t *Task, err error) error {
	return f(t)
}

// A Config defines options for interpreting an Instance as a Workflow.
type Config struct {
	// Root limits the search for tasks to be within the path indicated to root.
	// For the cue command, this is set to ["command"]. The default value is
	// for all tasks to be root.
	Root cue.Path

	// InferTasks allows tasks to be defined outside of the Root. Such tasks
	// will only be included in the workflow if any of its fields is referenced
	// by any of the tasks defined within Root.
	//
	// CAVEAT EMPTOR: this features is mostly provided for backwards
	// compatibility with v0.2. A problem with this approach is that it will
	// look for task structs within arbitrary data. So if not careful, there may
	// be spurious matches.
	InferTasks bool

	// IgnoreConcrete ignores references for which the values are already
	// concrete and cannot change.
	IgnoreConcrete bool

	FindHiddenTasks bool

	// UpdateFunc is called whenever the information in the controller is
	// updated. This includes directly after initialization. The task may be
	// nil if this call is not the result of a task completing.
	UpdateFunc func(c *Controller, t *Task) error
}

// A Controller defines a set of Tasks to be executed.
type Controller struct {
	cfg    Config
	isTask TaskFunc

	inst        cue.Value
	valueSeqNum int64

	env *adt.Environment

	conjuncts   []adt.Conjunct
	conjunctSeq int64

	taskCh chan *Task

	opCtx      *adt.OpContext
	context    context.Context
	cancelFunc context.CancelFunc

	// keys maps task keys to their index. This allows a recreation of the
	// Instance while retaining the original task indices.
	//
	// TODO: do instance updating in place to allow for more efficient
	// processing.
	keys  map[string]*Task
	tasks []*Task

	// Only used during task initialization.
	nodes map[*adt.Vertex]*Task

	errs errors.Error
}

// Tasks reports the tasks that are currently registered with the controller.
//
// This may currently only be called before Run is called or from within
// a call to UpdateFunc. Task pointers returned by this call are not guaranteed
// to be the same between successive calls to this method.
func (c *Controller) Tasks() []*Task {
	return c.tasks
}

func (c *Controller) cancel() {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
}

func (c *Controller) addErr(err error, msg string) {
	c.errs = errors.Append(c.errs, errors.Promote(err, msg))
}

// New creates a Controller for a given Instance and TaskFunc.
//
// The instance value can either be a *cue.Instance or a cue.Value.
func New(cfg *Config, inst cue.InstanceOrValue, f TaskFunc) *Controller {
	v := inst.Value()
	ctx := eval.NewContext(value.ToInternal(v))

	c := &Controller{
		isTask: f,
		inst:   v,
		opCtx:  ctx,

		taskCh: make(chan *Task),
		keys:   map[string]*Task{},
	}

	if cfg != nil {
		c.cfg = *cfg
	}

	c.initTasks()
	return c

}

// Run runs the tasks of a workflow until completion.
func (c *Controller) Run(ctx context.Context) error {
	c.context, c.cancelFunc = context.WithCancel(ctx)
	defer c.cancelFunc()

	c.runLoop()
	return c.errs
}

// A State indicates the state of a Task.
//
// The following state diagram indicates the possible state transitions:
//
//          Ready
//       ↗︎        ↘︎
//   Waiting  ←  Running
//       ↘︎        ↙︎
//       Terminated
//
// A Task may move from Waiting to Terminating if one of
// the tasks on which it dependends fails.
//
// NOTE: transitions from Running to Waiting are currently not supported. In
// the future this may be possible if a task depends on continuously running
// tasks that send updates.
//
type State int

const (
	// Waiting indicates a task is blocked on input from another task.
	//
	// NOTE: although this is currently not implemented, a task could
	// theoretically move from the Running to Waiting state.
	Waiting State = iota

	// Ready means a tasks is ready to run, but currently not running.
	Ready

	// Running indicates a goroutine is currently active for a task and that
	// it is not Waiting.
	Running

	// Terminated means a task has stopped running either because it terminated
	// while Running or was aborted by task on which it depends. The error
	// value of a Task indicates the reason for the termination.
	Terminated
)

var stateStrings = map[State]string{
	Waiting:    "Waiting",
	Ready:      "Ready",
	Running:    "Running",
	Terminated: "Terminated",
}

// String reports a human readable string of status s.
func (s State) String() string {
	return stateStrings[s]
}

// A Task contains the context for a single task execution.
// Tasks may be run concurrently.
type Task struct {
	// Static
	c    *Controller
	ctxt *adt.OpContext
	r    Runner

	index  int
	path   cue.Path
	key    string
	labels []adt.Feature

	// Dynamic
	update   adt.Expr
	deps     map[*Task]bool
	pathDeps map[string][]*Task

	conjunctSeq int64
	valueSeq    int64
	v           cue.Value
	err         errors.Error
	state       State
	depTasks    []*Task
}

// Context reports the Controller's Context.
func (t *Task) Context() context.Context {
	return t.c.context
}

// Path reports the path of Task within the Instance in which it is defined.
// The Path is always valid.
func (t *Task) Path() cue.Path {
	return t.path
}

// Index reports the sequence number of the Task. This will not change over
// time.
func (t *Task) Index() int {
	return t.index
}

func (t *Task) done() bool {
	return t.state > Running
}

func (t *Task) isReady() bool {
	for _, d := range t.depTasks {
		if !d.done() {
			return false
		}
	}
	return true
}

func (t *Task) vertex() *adt.Vertex {
	_, x := value.ToInternal(t.v)
	return x
}

func (t *Task) addDep(path string, dep *Task) {
	if dep == nil || dep == t {
		return
	}
	if t.deps == nil {
		t.deps = map[*Task]bool{}
		t.pathDeps = map[string][]*Task{}
	}

	// Add the dependencies for a given path to the controller. We could compute
	// this again later, but this ensures there will be no discrepancies.
	a := t.pathDeps[path]
	found := false
	for _, t := range a {
		if t == dep {
			found = true
			break
		}
	}
	if !found {
		t.pathDeps[path] = append(a, dep)

	}

	if !t.deps[dep] {
		t.deps[dep] = true
		t.depTasks = append(t.depTasks, dep)
	}
}

// Fill fills in values of the Controller's configuration for the current task.
// The changes take effect after the task completes.
//
// This method may currently only be called by the runner.
func (t *Task) Fill(x interface{}) error {
	expr := convert.GoValueToExpr(t.ctxt, true, x)
	if t.update == nil {
		t.update = expr
		return nil
	}
	t.update = &adt.BinaryExpr{
		Op: adt.AndOp,
		X:  t.update,
		Y:  expr,
	}
	return nil
}

// Value reports the latest value of this task.
//
// This method may currently only be called before Run is called or after a
// Task completed, or from within a call to UpdateFunc.
func (t *Task) Value() cue.Value {
	// TODO: synchronize
	return t.v
}

// Dependencies reports the Tasks t depends on.
//
// This method may currently only be called before Run is called or after a
// Task completed, or from within a call to UpdateFunc.
func (t *Task) Dependencies() []*Task {
	// TODO: add synchronization.
	return t.depTasks
}

// PathDependencies reports the dependencies found for a value at the given
// path.
//
// This may currently only be called before Run is called or from within
// a call to UpdateFunc.
func (t *Task) PathDependencies(p cue.Path) []*Task {
	return t.pathDeps[p.String()]
}

// Err returns the error of a completed Task.
//
// This method may currently only be called before Run is called, after a
// Task completed, or from within a call to UpdateFunc.
func (t *Task) Err() error {
	return t.err
}

// State is the current state of the Task.
//
// This method may currently only be called before Run is called or after a
// Task completed, or from within a call to UpdateFunc.
func (t *Task) State() State {
	return t.state
}
