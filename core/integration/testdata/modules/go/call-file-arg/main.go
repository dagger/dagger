package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(file *dagger.File) *dagger.File {
	return file
}
