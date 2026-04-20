package main

import "dagger/toolchain-generators/internal/dagger"

type ToolchainGenerators struct{}

// +generate
func (t *ToolchainGenerators) ToolchainFile() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("toolchain-from-dep", "generated").
		Changes(dag.Directory())
}
