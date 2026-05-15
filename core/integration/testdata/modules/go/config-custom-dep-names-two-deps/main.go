package main

import "context"

type Test struct{}

func (m *Test) Fn(ctx context.Context) (string, error) {
	dep1, err := dag.Foo().Fn(ctx)
	if err != nil {
		return "", err
	}
	dep2, err := dag.Bar().Fn(ctx)
	if err != nil {
		return "", err
	}
	return dep1 + " " + dep2, nil
}
