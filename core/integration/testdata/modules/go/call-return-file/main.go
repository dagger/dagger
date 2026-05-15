package main

import "dagger/test/internal/dagger"

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *dagger.File
}
