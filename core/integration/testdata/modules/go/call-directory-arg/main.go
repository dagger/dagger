package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(dir *dagger.Directory) *dagger.Directory {
	return dir
}
