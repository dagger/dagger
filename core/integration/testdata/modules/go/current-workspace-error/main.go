package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() *dagger.Workspace {
	return dag.CurrentWorkspace()
}
