package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) RedisService(ctx context.Context) (string, error) {
	// create Redis service container
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
		Stdout(ctx)

	// get value
	return redisCLI.
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)
}
