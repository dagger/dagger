package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

// Test returns ordinary core metadata values by ID, the way any module
// function hands existing core objects back to the engine.
type Test struct{}

const introspection = `{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[]}],"directives":[{"name":"sourceMap","description":"where a type came from","locations":["OBJECT"],"args":[]}]}}`

func (m *Test) EnvVar(ctx context.Context) (*dagger.EnvVariable, error) {
	vars, err := dag.Container().WithEnvVariable("CI", "true").EnvVariables(ctx)
	if err != nil {
		return nil, err
	}
	for i := range vars {
		name, err := vars[i].Name(ctx)
		if err != nil {
			return nil, err
		}
		if name == "CI" {
			return &vars[i], nil
		}
	}
	return nil, fmt.Errorf("CI variable not found")
}

func (m *Test) Ports(ctx context.Context) ([]*dagger.Port, error) {
	ports, err := dag.Container().
		WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{Description: "web"}).
		WithExposedPort(9090).
		ExposedPorts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*dagger.Port, 0, len(ports))
	for i := range ports {
		out = append(out, &ports[i])
	}
	return out, nil
}

func (m *Test) Schema() *dagger.Schema {
	return dag.Schema(dagger.JSON(introspection))
}

func (m *Test) Clients(ctx context.Context) ([]*dagger.ModuleConfigClient, error) {
	clients, err := dag.CurrentModule().Source().AsModuleSource().WithClient("go", "./gen").ConfigClients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*dagger.ModuleConfigClient, 0, len(clients))
	for i := range clients {
		out = append(out, &clients[i])
	}
	return out, nil
}
