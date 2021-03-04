package dagger

import (
	"context"

	cueflow "cuelang.org/go/tools/flow"
	"dagger.io/go/dagger/compiler"
	"github.com/opentracing/opentracing-go"
)

// Something which can be filled in-place with a cue value
type Fillable struct {
	t *cueflow.Task
	v *compiler.Value
}

func NewFillable(t *cueflow.Task, v *compiler.Value) *Fillable {
	return &Fillable{
		t: t,
		v: v,
	}
}

func (f *Fillable) Fill(ctx context.Context, x interface{}) error {
	// Use a nil pointer receiver to discard all values
	if f == nil {
		return nil
	}

	span, _ := opentracing.StartSpanFromContext(ctx, "task fill")
	defer span.Finish()

	if err := f.t.Fill(x); err != nil {
		return err
	}

	// Mirror the fill into the output
	return f.v.FillPath(x, f.t.Path())
}
