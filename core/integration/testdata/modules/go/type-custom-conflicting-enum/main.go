package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestBool(ctx context.Context) (string, error) {
	f, err := dag.Dep().Thing(ctx, dagger.DepMyEnumFalse)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(f), nil
}

func (m *Test) TestNull(ctx context.Context) (string, error) {
	f, err := dag.Dep().Thing(ctx, dagger.DepMyEnumNull)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(f), nil
}
