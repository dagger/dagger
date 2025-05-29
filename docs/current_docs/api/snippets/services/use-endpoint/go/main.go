package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Get(ctx context.Context) (string, error) {
	// Start NGINX service
	svc := dag.Container().From("nginx").WithExposedPort(80).AsService()
	svc, err := svc.Start(ctx)
	if err != nil {
		return "", err
	}

	// Wait for service endpoint
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http", Port: 80})
	if err != nil {
		return "", err
	}

	// Send HTTP request to service endpoint
	return dag.HTTP(endpoint).Contents(ctx)
}
