package main

// This module exercises type resolution paths the original
// string-based resolver couldn't follow but the go/types-based one
// handles transparently:
//
//   - context.Context referenced through a local type alias
//   - a Dagger type referenced through a local type alias
//
// The expected output below confirms the alias is resolved to its
// target: the Context arg is dropped, and the *Container return is
// recognised as the dagger.Container OBJECT.

import (
	"context"

	"dagger.io/dagger"
)

type Ctx = context.Context

type LocalContainer = *dagger.Container

type Aliases struct{}

func (a *Aliases) Fn(ctx Ctx, name string) LocalContainer {
	return nil
}
