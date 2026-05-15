package main

import "dagger/test/internal/dagger"

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *dagger.Directory
}
