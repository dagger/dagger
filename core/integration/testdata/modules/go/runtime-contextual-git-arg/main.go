package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	//+private
	Ref *dagger.GitRef
	//+private
	Dep *dagger.Dep
}

func New(
	// +defaultPath="."
	ref *dagger.GitRef,
	//+defaultPath="crap"
	source *dagger.Directory,
) *Test {
	return &Test{
		Ref: ref,
		Dep: dag.Dep(source),
	}
}

func (m *Test) Fn(
	ctx context.Context,
	//+defaultPath="config/config.local.js"
	configFile *dagger.File,
) (*dagger.Directory, error) {
	return m.Dep.WithRef(m.Ref).Fn().WithFile("config.js", configFile).Sync(ctx)
}
