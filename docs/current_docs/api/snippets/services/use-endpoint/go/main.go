package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Get(ctx context.Context) (string, error) {
	// Start NGINX service
	s := dag.Container().From("nginx").WithExposedPort(80).AsService()
	s, err := s.Start(ctx)
	if err != nil {
		return "", err
	}

	// Wait for service endpoint
	ep, err := s.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http", Port: 80})
	if err != nil {
		return "", err
	}

	// Send HTTP request to service endpoint
	return dag.HTTP(ep).Contents(ctx)
}
