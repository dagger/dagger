package main

import "dagger/test/internal/dagger"

type Test struct{}

func (t *Test) Hello() string {
	return "hello"
}

func (t *Test) File() *dagger.File {
	return dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt")
}
