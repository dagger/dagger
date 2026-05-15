package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	Obj *Obj
}

func New(
	// +defaultPath="/crap"
	dir *dagger.Directory,
) *Test {
	return &Test{Obj: &Obj{Dir: dir}}
}

type Obj struct {
	Dir *dagger.Directory
}

func (o *Obj) Foo(ctx context.Context) (string, error) {
	return o.Dir.File("foo.txt").Contents(ctx)
}
