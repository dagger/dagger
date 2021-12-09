package task

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/stdlib"
)

var (
	ErrNotTask = errors.New("not a task")
	tasks      sync.Map
	typePath   = cue.MakePath(cue.Hid("_type", stdlib.EnginePackage))
)

type NewFunc func() Task

type Task interface {
	Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error)
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
	// FIXME: legacy pipelines
	if environment.IsComponent(v) {
		return New("#up"), nil
	}

	if v.Kind() != cue.StructKind {
		return nil, ErrNotTask
	}

	typ := v.LookupPath(typePath)
	if !typ.Exists() {
		return nil, ErrNotTask
	}

	typeString, err := typ.String()
	if err != nil {
		return nil, err
	}

	t := New(typeString)
	if t == nil {
		return nil, fmt.Errorf("unknown type %q", typeString)
	}
	return t, nil
}
