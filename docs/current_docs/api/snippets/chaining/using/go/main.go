package main

import (
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Example(buildSrc *dagger.Directory, buildArgs []string) *dagger.Directory {
	return dag.
		Golang().
		Build(buildArgs, dagger.GolangBuildOpts{Source: buildSrc}).
		Terminal()
}
