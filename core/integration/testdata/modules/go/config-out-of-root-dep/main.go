package main

import (
	"context"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) GetSource(ctx context.Context) *dagger.Directory {
	return dag.CurrentModule().Source()
}
