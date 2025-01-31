package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/.dagger/build"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type Codegen struct {
	CodegenBin        *dagger.File
	Module            *dagger.Directory
	IntrospectionJSON *dagger.File
	ModuleName        string
}

func NewCodegen(
	ctx context.Context,
	source *dagger.Directory,
	module *dagger.Directory,
	introspectionJSON *dagger.File,
	moduleName string,
) (*Codegen, error) {
	builder, err := build.NewBuilder(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	return &Codegen{
		CodegenBin:        builder.CodegenBinary(),
		Module:            module,
		IntrospectionJSON: introspectionJSON,
		ModuleName:        moduleName,
	}, nil
}

func (c *Codegen) Typescript() (*dagger.File, error) {
	return c.base().
		WithExec([]string{
			"codegen", "--lang", "typescript",
			"-o", "/out",
			"--introspection-json-path", "/introspection.json",
		}).
		Directory("/out").
		File("client.gen.ts"), nil
}

func (c *Codegen) Go() (*dagger.Directory, error) {
	return c.base().
		WithExec([]string{
			"codegen", "--lang", "go",
			"-o", "/app",
			"--introspection-json-path", "/introspection.json",
			"--module-context-path", "/app",
			"--module-name", c.ModuleName,
		}).
		Directory("/app/internal"), nil
}

func (c *Codegen) base() *dagger.Container {
	base := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"go"},
		}).
		WithFile("/usr/local/bin/codegen", c.CodegenBin).
		WithDirectory("/app", c.Module).
		WithWorkdir("/app")

	if c.IntrospectionJSON != nil {
		base = base.WithFile("/introspection.json", c.IntrospectionJSON)
	}

	return base
}
