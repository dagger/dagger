package main

import (
	"context"
	"dagger/my-module/internal/dagger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
		// create a span for each file creation operation
		_, span := Tracer().Start(ctx, "create-file", trace.WithAttributes(attribute.String("file.name", name)))
		defer span.End()
		// create the file and add it to the container
		container = container.WithNewFile(name, content)
	}
	return container.Directory("/results")
}
