package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() (*dagger.DepObj, error) {
	return nil, nil
}
