package dagger

import (
	"context"
)

// Implemented by Component, Script, Op
type Executable interface {
	Execute(context.Context, FS, *Fillable) (FS, error)
	Walk(context.Context, func(*Op) error) error
}
