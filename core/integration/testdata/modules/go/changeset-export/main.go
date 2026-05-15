package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		Dir: dag.Directory().
			WithNewFile("foo.txt", "foo\nbar\nbaz").
			WithNewFile("bar.txt", "hey").
			WithNewDirectory("emptydir"),
	}
}

type Test struct {
	Dir *dagger.Directory
}

func (t *Test) Update() *dagger.Changeset {
	return t.Dir.
		WithNewFile("foo.txt", "foo\nbaz").
		WithoutFile("bar.txt").
		WithNewFile("baz.txt", "im new here").
		WithoutDirectory("emptydir").
		Changes(t.Dir)
}

func (t *Test) NoChanges() *dagger.Changeset {
	return t.Dir.Changes(t.Dir)
}
