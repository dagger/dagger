package main

import (
	"context"
	"dagger/hello/internal/dagger"
)

type Hello struct {
	Config *dagger.File
}

func New(
	// +defaultPath="./app-config.txt"
	config *dagger.File,
) *Hello {
	return &Hello{
		Config: config,
	}
}

func (m *Hello) Message() string {
	return "hello from blueprint"
}

// This should read Hello's own config file
func (m *Hello) BlueprintConfig(ctx context.Context) (string, error) {
	return dag.CurrentModule().Source().File("blueprint-config.txt").Contents(ctx)
}

// This should return the target module's name, not Hello's
func (m *Hello) FieldConfig(ctx context.Context) (string, error) {
	return m.Config.Contents(ctx)
}
