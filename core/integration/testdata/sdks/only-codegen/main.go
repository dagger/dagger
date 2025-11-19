package main

import (
	"context"
	"dagger/only-codegen/internal/dagger"
)

type OnlyCodegen struct{}

func (o *OnlyCodegen) Codegen(
	ctx context.Context, //nolint:unparam
	modSource *dagger.ModuleSource, //nolint:unparam
	introspectionJSON *dagger.File, //nolint:unparam
) (*dagger.GeneratedCode, error) {
	return dag.GeneratedCode(dag.Directory().WithNewFile("hello.txt", "Hello, world!")), nil
}
