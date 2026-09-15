package main

import "context"

type Caller struct{}

func (m *Caller) ViaDep(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().Print(ctx, "direct-"+stringArg)
}

func (m *Caller) ViaDepContainer(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().ViaSelfContainer("container-" + stringArg).Stdout(ctx)
}

func (m *Caller) ViaDepWorker(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().Worker().Echo(ctx, "worker-"+stringArg)
}
