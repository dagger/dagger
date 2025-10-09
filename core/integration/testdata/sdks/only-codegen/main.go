package main

import (
	"context"
	"dagger/only-codegen/internal/dagger"
)

type OnlyCodegen struct{}

func (o *OnlyCodegen) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	return dag.GeneratedCode(dag.Directory().WithNewFile("hello.txt", "Hello, world!")), nil
}
