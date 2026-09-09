package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Directories() []*dagger.Directory {
	return []*dagger.Directory{
		dag.Directory().WithNewFile("one.txt", "first"),
		dag.Directory().WithNewFile("two.txt", "second"),
	}
}
