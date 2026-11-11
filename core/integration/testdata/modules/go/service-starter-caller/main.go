package main

import (
	"context"
	"fmt"
	"time"

	"dagger/caller/internal/dagger"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) (string, error) {
	cache := dag.CacheVolume("svc_cache")

	_, err := dag.Container().From("alpine").WithMountedCache("/cache", cache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"sh", "-c", "truncate -s 0 /cache/svc.txt && rm -f /cache/bound"}).
		Sync(ctx)
	if err != nil {
		return "", err
	}

	svc := dag.Container().From("alpine").
		WithMountedCache("/cache", cache).
		WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{ExperimentalSkipHealthcheck: true}).
		WithDefaultArgs([]string{"sh", "-c", "echo started >> /cache/svc.txt && while true; do wc -l /cache/svc.txt && sleep 5; done"}).
		AsService()

	// A different module, so a different client, brings the service up
	// through a tunnel, and keeps it up until this client has bound it too.
	starterDone := make(chan error, 1)
	go func() { starterDone <- dag.Starter().Start(ctx, svc, cache) }()

	// Wait until the starter's tunnel has started the service.
	_, err = dag.Container().From("alpine").WithMountedCache("/cache", cache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"timeout", "120", "sh", "-c", "until grep -q started /cache/svc.txt; do sleep 0.1; done"}).
		Sync(ctx)
	if err != nil {
		select {
		case starterErr := <-starterDone:
			return "", fmt.Errorf("starter: %w", starterErr)
		default:
			return "", err
		}
	}

	// Bind the service as this client while the starter keeps it up, then
	// let the starter return.
	_, err = dag.Container().From("alpine").WithServiceBinding("db", svc).
		WithMountedCache("/cache", cache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"touch", "/cache/bound"}).
		Sync(ctx)
	if err != nil {
		return "", err
	}
	if err := <-starterDone; err != nil {
		return "", err
	}

	return dag.Container().From("alpine").WithMountedCache("/cache", cache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"wc", "-l", "/cache/svc.txt"}).Stdout(ctx)
}
