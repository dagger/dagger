package dagger

import (
	cueflow "cuelang.org/go/tools/flow"
)

// Something which can be filled in-place with a cue value
type Fillable struct {
	cc *Compiler // for locking
	t  *cueflow.Task
}

func NewFillable(cc *Compiler, t *cueflow.Task) *Fillable {
	return &Fillable{
		cc: cc,
		t:  t,
	}
}

func (f *Fillable) Fill(x interface{}) error {
	if f == nil {
		return nil
	}
	f.cc.Lock()
	err := f.t.Fill(x)
	f.cc.Unlock()
	return err
}
