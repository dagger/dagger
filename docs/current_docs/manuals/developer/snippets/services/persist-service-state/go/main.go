package main

import (
	"context"
)

type MyModule struct{}

// Create Redis service and client
func (m *MyModule) Redis(ctx context.Context) *Container {
	redisSrv := dag.Container().
		From("redis").
		WithExposedPort(6379).
		WithMountedCache("/data", dag.CacheVolume("my-redis")).
		WithWorkdir("/data").
		AsService()

	redisCLI := dag.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	return redisCLI
}

// Set key and value in Redis service
func (m *MyModule) Set(
	ctx context.Context,
	// The cache key to set
	key string,
	// The cache value to set
	value string,
) (string, error) {
	return m.Redis(ctx).
		WithExec([]string{"set", key, value}).
		WithExec([]string{"save"}).
		Stdout(ctx)
}

// Get value from Redis service
func (m *MyModule) Get(
	ctx context.Context,
	// The cache value to set
	key string,
) (string, error) {
	return m.Redis(ctx).
		WithExec([]string{"get", key}).
		Stdout(ctx)
}
