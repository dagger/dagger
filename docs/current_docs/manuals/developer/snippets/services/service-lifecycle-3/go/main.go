package main

import (
	"context"
)

type MyModule struct{}

// creates Redis service and client
func (m *MyModule) RedisService(ctx context.Context) (string, error) {
	redisSrv := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService()

	// create Redis client container
	redisCLI := dag.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	// set and get value
	return redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)
}
