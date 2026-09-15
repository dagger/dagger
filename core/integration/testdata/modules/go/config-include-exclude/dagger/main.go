package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() *dagger.Directory {
	return dag.CurrentModule().Source()
}
