package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ModSrc(ctx context.Context, modSrc *dagger.ModuleSource) *dagger.ModuleSource {
	return modSrc
}

func (m *Test) Mod(ctx context.Context, module *dagger.Module) *dagger.Module {
	return module
}
