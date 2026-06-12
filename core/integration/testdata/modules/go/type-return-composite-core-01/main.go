package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) MySlice() []*dagger.Container {
	return []*dagger.Container{dag.Container().From("alpine:3.22.1").WithExec([]string{"echo", "hello world"})}
}

type Foo struct {
	Con *dagger.Container
	// verify fields can remain nil w/out error too
	UnsetFile *dagger.File
}

func (m *Test) MyStruct() *Foo {
	return &Foo{Con: dag.Container().From("alpine:3.22.1").WithExec([]string{"echo", "hello world"})}
}
