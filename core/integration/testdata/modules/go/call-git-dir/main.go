package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(
	dir *dagger.Directory,
	subpath string, // +optional
) *dagger.Directory {
	if subpath == "" {
		subpath = "."
	}
	return dir.Directory(subpath)
}
