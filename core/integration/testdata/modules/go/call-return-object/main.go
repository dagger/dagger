package main

import "dagger/test/internal/dagger"

func New() *Test {
	return &Test{BaseImage: "alpine:3.22.1"}
}

type Test struct {
	BaseImage string
}

func (t *Test) Foo() *Foo {
	return &Foo{Ctr: dag.Container().From(t.BaseImage)}
}

func (t *Test) Files() []*dagger.File {
	return []*dagger.File{
		dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
		dag.Directory().WithNewFile("bar.txt", "bar").File("bar.txt"),
	}
}

func (*Test) Deploy() string {
	return "here be dragons!"
}

type Foo struct {
	Ctr *dagger.Container
}
