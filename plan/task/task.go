package task

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

var (
	ErrNotTask = errors.New("not a task")
	tasks      sync.Map
	typePath   = cue.MakePath(
		cue.Str("$dagger"),
		cue.Str("task"),
		cue.Hid("_name", pkg.DaggerPackage))
	corePath = cue.MakePath(
		cue.Str("$dagger"),
		cue.Str("task"),
		cue.Hid("_name", pkg.DaggerCorePackage))
	paths = []cue.Path{corePath, typePath}
)

// State is the state of the task.
type State int8

func (s State) String() string {
	return [...]string{"computing", "completed", "cancelled", "failed"}[s]
}

func ParseState(s string) (State, error) {
	switch s {
	case "computing":
		return StateComputing, nil
	case "cancelled":
		return StateCanceled, nil
	case "failed":
		return StateFailed, nil
	case "completed":
		return StateCompleted, nil
	}

	return -1, fmt.Errorf("invalid state [%s]", s)
}

func (s State) CanTransition(t State) bool {
	return s <= t
}

const (
	// state order is important here since it defines the  order
	// on how states can transition only forwards
	// computing > completed > canceled > failed
	StateComputing State = iota
	StateCompleted
	StateCanceled
	StateFailed
)

type NewFunc func() Task

type Task interface {
	Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error)
}

type PreRunner interface {
	Task

	PreRun(ctx context.Context, pctx *plancontext.Context, v *compiler.Value) error
}

// Register a task type and initializer
func Register(typ string, f NewFunc) {
	tasks.Store(typ, f)
}

// New creates a new Task of the given type.
func New(typ string) Task {
	v, ok := tasks.Load(typ)
	if !ok {
		return nil
	}
	fn := v.(NewFunc)
	return fn()
}

func Lookup(v *compiler.Value) (Task, error) {
	if v.Kind() != cue.StructKind {
		return nil, ErrNotTask
	}

	typeString, err := lookupType(v)
	if err != nil {
		return nil, err
	}

	t := New(typeString)
	if t == nil {
		return nil, fmt.Errorf("unknown type %q", typeString)
	}

	return t, nil
}

func lookupType(v *compiler.Value) (string, error) {
	for _, path := range paths {
		typ := v.LookupPath(path)
		if typ.Exists() {
			return typ.String()
		}
	}
	return "", ErrNotTask
}
