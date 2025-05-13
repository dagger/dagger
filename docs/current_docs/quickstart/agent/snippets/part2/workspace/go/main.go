// A module for editing code

package main

import (
	"context"
	"dagger/workspace/internal/dagger"
)

type Workspace struct {
	Source *dagger.Directory
}

func New(
	// The source directory
	source *dagger.Directory,
) *Workspace {
	return &Workspace{Source: source}
}

// Read a file in the Workspace
func (w *Workspace) ReadFile(
	ctx context.Context,
	// The path to the file in the workspace
	path string,
) (string, error) {
	return w.Source.File(path).Contents(ctx)
}

// Write a file to the Workspace
func (w *Workspace) WriteFile(
	// The path to the file in the workspace
	path string,
	// The new contents of the file
	contents string,
) *Workspace {
	w.Source = w.Source.WithNewFile(path, contents)
	return w
}

// List all of the files in the Workspace
func (w *Workspace) ListFiles(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:3").
		WithDirectory("/src", w.Source).
		WithWorkdir("/src").
		WithExec([]string{"tree", "./src"}).
		Stdout(ctx)
}

// Get the source code directory from the Workspace
func (w *Workspace) GetSource() *dagger.Directory {
	return w.Source
}
