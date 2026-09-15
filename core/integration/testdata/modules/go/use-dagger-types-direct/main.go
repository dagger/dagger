package main

import "dagger/minimal/internal/dagger"

type Minimal struct{}

func (m *Minimal) Foo(dir *dagger.Directory) *dagger.Directory {
	return dir.WithNewFile("foo", "xxx")
}

func (m *Minimal) Bar(dir *dagger.Directory) *dagger.Directory {
	return dir.WithNewFile("bar", "yyy")
}
