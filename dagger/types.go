package dagger

import (
	"context"
)

// Implemented by Component, Script, Op
type Executable interface {
	Execute(context.Context, FS, Fillable) (FS, error)
	Walk(context.Context, func(*Op) error) error
}

// Something which can be filled in-place with a cue value
type Fillable interface {
	Fill(interface{}) error
}

func Discard() Fillable {
	return discard{}
}

type discard struct{}

func (d discard) Fill(x interface{}) error {
	return nil
}
