package main

import (
	"context"
)

type Test struct{}

func (m *Test) TestRefLocal(ctx context.Context) (string, error) {
	return dag.ContextGit().TestRefLocal(ctx)
}
