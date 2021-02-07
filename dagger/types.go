package dagger

import (
	"context"
	"fmt"
	"os"

	cueflow "cuelang.org/go/tools/flow"

	"dagger.cloud/go/dagger/cc"
)

var ErrNotExist = os.ErrNotExist

// Implemented by Component, Script, Op
type Executable interface {
	Execute(context.Context, FS, *Fillable) (FS, error)
	Walk(context.Context, func(*Op) error) error
}

func newExecutable(v *cc.Value) (Executable, error) {
	// NOTE: here we need full spec validation,
	//   so we call NewScript, NewComponent, NewOp.
	if script, err := NewScript(v); err == nil {
		return script, nil
	}
	if component, err := NewComponent(v); err == nil {
		return component, nil
	}
	if op, err := NewOp(v); err == nil {
		return op, nil
	}
	return nil, fmt.Errorf("value is not executable")
}

// Something which can be filled in-place with a cue value
type Fillable struct {
	t *cueflow.Task
}

func NewFillable(t *cueflow.Task) *Fillable {
	return &Fillable{
		t: t,
	}
}

func (f *Fillable) Fill(x interface{}) error {
	// Use a nil pointer receiver to discard all values
	if f == nil {
		return nil
	}
	return f.t.Fill(x)
}
