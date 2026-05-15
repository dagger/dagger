package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.Foo().DepFn(ctx)
}

func (m *Test) GetObj(ctx context.Context) (string, error) {
	var obj *dagger.FooObj
	obj = dag.Foo().GetDepObj()
	return obj.Str(ctx)
}

func (m *Test) GetOtherObj(ctx context.Context) (string, error) {
	var obj *dagger.FooOtherObj
	obj = dag.Foo().GetOtherObj()
	return obj.Str(ctx)
}

func (m *Test) GetConflictNameObj(ctx context.Context) *Dep {
	return &Dep{Str: "it worked?"}
}

type Dep struct {
	Str string
}
