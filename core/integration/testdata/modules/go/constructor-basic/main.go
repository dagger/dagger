package main

import (
	"context"
	"dagger/test/internal/dagger"
)

func New(
	ctx context.Context,
	foo string,
	bar *int, // +optional
	baz []string,
	dir *dagger.Directory,
) *Test {
	bar2 := 42
	if bar != nil {
		bar2 = *bar
	}
	return &Test{
		Foo: foo,
		Bar: bar2,
		Baz: baz,
		Dir: dir,
	}
}

type Test struct {
	Foo         string
	Bar         int
	Baz         []string
	Dir         *dagger.Directory
	NeverSetDir *dagger.Directory
}

func (m *Test) GimmeFoo() string {
	return m.Foo
}

func (m *Test) GimmeBar() int {
	return m.Bar
}

func (m *Test) GimmeBaz() []string {
	return m.Baz
}

func (m *Test) GimmeDirEnts(ctx context.Context) ([]string, error) {
	return m.Dir.Entries(ctx)
}
