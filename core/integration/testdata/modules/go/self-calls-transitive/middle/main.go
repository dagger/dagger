package main

import "context"

type Middle struct{}

func (m *Middle) ViaDep(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().Print(ctx, "transitive-"+stringArg)
}
