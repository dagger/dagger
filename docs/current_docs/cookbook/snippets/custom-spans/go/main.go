package main

import (
	"context"
	"dagger/my-module/internal/dagger"

	"dagger.io/dagger/dag"
	"go.opentelemetry.io/otel/codes"
)

type MyModule struct{}

// Create an alpine container, write three files, and emit custom spans for each
func (m *MyModule) Foo(ctx context.Context) *dagger.Directory {
	// define the files to be created and their contents
	files := map[string]string{
		"file1.txt": "foo",
		"file2.txt": "bar",
		"file3.txt": "baz",
	}

	// set up an alpine container with the directory mounted
	container := dag.Container().
		From("alpine:latest").
		WithDirectory("/results", dag.Directory()).
		WithWorkdir("/results")
	for name, content := range files {
		// create files
		container = container.WithNewFile(name, content)
		// emit custom spans for each file created
		log := "Created file: " + name + " with contents: " + content
		_, span := Tracer().Start(ctx, log)
		span.SetStatus(codes.Ok, "STATUS")
		span.End()
	}
	return container.Directory("/results")
}
