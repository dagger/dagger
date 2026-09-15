package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ExplicitWorkspaceArg(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	return dag.Dep().ReadWorkspaceArg(ctx, dagger.DepReadWorkspaceArgOpts{
		Workspace: workspace,
	})
}

func (m *Test) ImplicitWorkspaceArg(ctx context.Context) (string, error) {
	return dag.Dep().ReadWorkspaceArg(ctx)
}
