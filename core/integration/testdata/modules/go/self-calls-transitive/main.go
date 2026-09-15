package main

import "context"

type Caller struct{}

func (m *Caller) ViaTransitiveDep(ctx context.Context, stringArg string) (string, error) {
	return dag.Middle().ViaDep(ctx, stringArg)
}
