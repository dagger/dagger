package dagger

import (
	"os"

	cueflow "cuelang.org/go/tools/flow"
)

var ErrNotExist = os.ErrNotExist

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
