package main

import "dagger/test/internal/dagger"

func New() *Test {
	return &Test{Ctr: dag.Container().From("alpine:3.22.1").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *dagger.Container
}
