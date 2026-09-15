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

func (*Test) Nop(ctx context.Context) (string, error) {
	return "nop", nil
}

func (*Test) Nop2(ctx context.Context) (string, error) {
	return "nop2", nil
}

type Obj struct {
	// +private
	Dir *dagger.Directory
}

func (o *Obj) Ents(ctx context.Context) ([]string, error) {
	return o.Dir.Entries(ctx)
}
