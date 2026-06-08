package main

import (
	"context"
)

type Usage struct{}

func (m *Usage) Test(ctx context.Context) (string, error) {
	// Because `Example` implements `Fooer`, Go codegen generates a local
	// adapter method. This is not a GraphQL call.
	return dag.MyModule().Foo(ctx, dag.Example().AsMyModuleFooer())
}
