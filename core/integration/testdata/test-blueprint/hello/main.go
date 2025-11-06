package main

import (
	"context"
	"dagger/hello/internal/dagger"
	"fmt"
)

type Hello struct{}

func (m *Hello) Message() string {
	return "hello from blueprint"
}

func (m *Hello) ConfigurableMessage(
	// +default="hello"
	message string,
) string {
	return fmt.Sprintf("%s from blueprint", message)
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

func (m *Hello) Greet() *Greetings {
	return &Greetings{}
}

type Greetings struct{}

func (p *Greetings) Planet(
	ctx context.Context,
	// +default="Earth"
	planet string,
) string {
	return fmt.Sprintf("Greetings from %s!", planet)
}
