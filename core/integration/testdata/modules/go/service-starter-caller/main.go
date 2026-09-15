package main

import (
	"context"
	"time"

	"dagger/caller/internal/dagger"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) (string, error) {
	svcCache := dag.CacheVolume("svc_cache")

	dag.Container().From("alpine").WithMountedCache("/cache", svcCache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"truncate", "-s", "0", "/cache/svc.txt"}).Sync(ctx)

	svc := dag.Container().From("alpine").
		WithMountedCache("/cache", svcCache).
		WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{ExperimentalSkipHealthcheck: true}).
		WithDefaultArgs([]string{"sh", "-c", "echo started >> /cache/svc.txt && while true; do wc -l /cache/svc.txt && sleep 5; done"}).
		AsService()

	// we're passing the service to a different module that calls "up" on it so it's a different client
	// which is what triggers the behavior this test is testing.
	dag.Starter().Start(ctx, svc)

	dag.Container().From("alpine").WithServiceBinding("db", svc).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"echo", "hello"}).
		Sync(ctx)

	return dag.Container().From("alpine").WithMountedCache("/cache", svcCache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"wc", "-l", "/cache/svc.txt"}).Stdout(ctx)
}
