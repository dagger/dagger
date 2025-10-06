package main

import (
	"context"
	"dagger/toy-workspace/internal/dagger"
)

// A toy workspace that can edit files and run 'go build'
type ToyWorkspace struct {
	// The workspace's container state.
	// +internal-use-only
	Container *dagger.Container
}

func New() ToyWorkspace {
	return ToyWorkspace{
		// Build a base container optimized for Go development
		Container: dag.Container().
			From("golang").
			WithDefaultTerminalCmd([]string{"/bin/bash"}).
			WithMountedCache("/go/pkg/mod", dag.CacheVolume("go_mod_cache")).
			WithWorkdir("/app").
			WithExec([]string{"go", "mod", "init", "main"}),
	}
}

// Read a file
func (w *ToyWorkspace) Read(ctx context.Context) (string, error) {
	return w.Container.File("main.go").Contents(ctx)
}

// Write a file
func (w ToyWorkspace) Write(content string) ToyWorkspace {
	w.Container = w.Container.WithNewFile("main.go", content)
	return w
}

// Build the code at the current directory in the workspace
func (w *ToyWorkspace) Build(ctx context.Context) error {
	_, err := w.Container.WithExec([]string{"go", "build", "./..."}).Stderr(ctx)
	return err
}
