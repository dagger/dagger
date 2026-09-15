package main

import (
	"context"
)

type Test struct{}

func (m *Test) TestRepoLocal(ctx context.Context) (string, error) {
	return dag.ContextGit().TestRepoLocal(ctx)
}

func (m *Test) TestRepoRemote(ctx context.Context) (string, error) {
	return dag.ContextGit().TestRepoRemote(ctx)
}

func (m *Test) TestRefLocal(ctx context.Context) (string, error) {
	return dag.ContextGit().TestRefLocal(ctx)
}

func (m *Test) TestRefRemote(ctx context.Context) (string, error) {
	return dag.ContextGit().TestRefRemote(ctx)
}
