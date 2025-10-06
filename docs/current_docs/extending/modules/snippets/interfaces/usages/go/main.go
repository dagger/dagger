package main

import (
	"context"
)

type Usage struct{}

func (m *Usage) Test(ctx context.Context) (string, error) {
	// Because `Example` implements `Fooer`, the conversion function 
	// `AsMyModuleFooer` has been generated.
	return dag.MyModule().Foo(ctx, dag.Example().AsMyModuleFooer())
}
