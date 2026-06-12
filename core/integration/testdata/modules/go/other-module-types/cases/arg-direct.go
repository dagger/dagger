package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(obj *dagger.DepObj) error {
	return nil
}
