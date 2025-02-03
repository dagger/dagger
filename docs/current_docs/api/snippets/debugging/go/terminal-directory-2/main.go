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
		Terminal(dagger.ContainerTerminalOpts{
			Cmd:                           []string{"/bin/bash"},
			ExperimentalPrivilegedNesting: false,
			InsecureRootCapabilities:      false,
		}).
		File("README.md").
		Contents(ctx)
}
