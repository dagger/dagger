package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) Test() *dagger.Directory {
	return dag.Depdep().Test()
}

func (m *Dep) TestFile() *dagger.Directory {
	return dag.Depdep().TestFile()
}
