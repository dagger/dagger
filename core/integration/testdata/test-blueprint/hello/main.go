package main

import (
	"context"
	"dagger/hello/internal/dagger"
)

type Hello struct{}

func (m *Hello) Message() string {
	return "hello from blueprint"
}

// This should read Hello's own config file
func (m *Hello) BlueprintConfig(ctx context.Context) (string, error) {
	return dag.CurrentModule().Source().File("blueprint-config.txt").Contents(ctx)
}

// This should return the target module's name, not Hello's
func (m *Hello) AppConfig(
	ctx context.Context,
	// +defaultPath="./app-config.txt"
	config *dagger.File,
) (string, error) {
	return config.Contents(ctx)
}
