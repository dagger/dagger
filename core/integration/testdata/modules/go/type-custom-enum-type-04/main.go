package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Status dagger.DepStatus // +private
}

func New() *Test {
	return &Test{Status: dagger.DepStatusActive}
}

func (m *Test) Active() string {
	return string(m.Status)
}

func (m *Test) Inactive(ctx context.Context) (string, error) {
	status, err := dag.Dep().Active(ctx)
	if err != nil {
		return "", err
	}
	status, err = dag.Dep().Invert(ctx, status)
	if err != nil {
		return "", err
	}
	return string(status), nil
}
