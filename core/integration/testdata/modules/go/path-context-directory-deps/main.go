package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) GetDepSource() *dagger.Directory {
	return dag.Dep().GetSource()
}

func (m *Test) GetRelDepSource() *dagger.Directory {
	return dag.Dep().GetRelSource()
}
