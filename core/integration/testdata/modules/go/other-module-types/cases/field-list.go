package main

import "dagger/test/internal/dagger"

type Test struct{}

type Obj struct {
	Foo []*dagger.DepObj
}

func (m *Test) Fn() (*Obj, error) {
	return nil, nil
}
