package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) AdvancedDirectory(ctx context.Context) (string, error) {
	return dag.
		Git("https://github.com/dagger/dagger.git").
		Head().
		Tree().
		Terminal(dagger.DirectoryTerminalOpts{
			Container: dag.Container().From("ubuntu"),
			Cmd:       []string{"/bin/bash"},
		}).
		File("README.md").
		Contents(ctx)
}
