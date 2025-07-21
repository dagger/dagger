package main

import (
	"context"
	"dagger/myblueprint/internal/dagger"
)

type Blueprint struct{}

func (m *Blueprint) Hello() string {
	return "hello from blueprint"
}

// This should read blueprint's own config file
func (m *Blueprint) BlueprintConfig(ctx context.Context) (string, error) {
	return dag.CurrentModule().Source().File("blueprint-config.txt").Contents(ctx)
}

// This should return the target module's name, not blueprint's
func (m *Blueprint) AppConfig(
	ctx context.Context,
	// +defaultPath="./app-config.txt"
	config *dagger.File,
) (string, error) {
	return config.Contents(ctx)
}
