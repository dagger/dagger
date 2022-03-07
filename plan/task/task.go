package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	lookups = []LookupFunc{
		defaultLookup,
		pathLookup,
	}
)

// State is the state of the task.
type State string

const (
	StateComputing = State("computing")
	StateCanceled  = State("canceled")
	StateFailed    = State("failed")
	StateCompleted = State("completed")
)

type NewFunc func() Task
type LookupFunc func(*compiler.Value) (Task, error)

type Task interface {
	Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error)
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
	for _, lookup := range lookups {
		t, err := lookup(v)
		if err != nil {
			return nil, err
		}
		if t != nil {
			return t, nil
		}
	}
	return nil, ErrNotTask
}

func defaultLookup(v *compiler.Value) (Task, error) {
	if v.Kind() != cue.StructKind {
		return nil, nil
	}

	typ := v.LookupPath(typePath)
	if !typ.Exists() {
		return nil, nil
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

func pathLookup(v *compiler.Value) (Task, error) {
	selectors := v.Path().Selectors()

	// The `actions` field won't have any path based tasks since it's in user land
	if len(selectors) == 0 || selectors[0].String() == "actions" {
		return nil, nil
	}

	// Try an exact match first
	if t := New(v.Path().String()); t != nil {
		return t, nil
	}

	// FIXME: is there a way to avoid having to loop here?
	var t Task
	tasks.Range(func(key, value interface{}) bool {
		if matchPathMask(selectors, key.(string)) {
			fn := value.(NewFunc)
			t = fn()
			return false
		}
		return true
	})
	return t, nil
}

func matchPathMask(sels []cue.Selector, mask string) bool {
	parts := strings.Split(mask, ".")
	if len(sels) != len(parts) {
		return false
	}
	for i, sel := range sels {
		// use a '*' in a path mask part to match any selector
		if parts[i] == "*" {
			continue
		}
		if sel.String() != parts[i] {
			return false
		}
	}
	return true
}
