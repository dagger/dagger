package main

import (
	"context"
)

type MyModule struct{}

// Create Redis service and client
func (m *MyModule) RedisService(ctx context.Context) (string, error) {
	redisSrv := dag.Container().
		From("redis").
		WithExposedPort(6379).
		WithMountedCache("/data", dag.CacheVolume("my-redis")).
		WithWorkdir("/data").
		AsService()

	// create Redis client container
	redisCLI := dag.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	// set and save value
	redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		WithExec([]string{"save"}).
		Sync(ctx)

	// get value
	return redisCLI.
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)
}
