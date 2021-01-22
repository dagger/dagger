package dagger

import (
	"context"

	cueflow "cuelang.org/go/tools/flow"
)

// Implemented by Component, Script, Op
type Executable interface {
	Execute(context.Context, FS, *Fillable) (FS, error)
	Walk(context.Context, func(*Op) error) error
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
