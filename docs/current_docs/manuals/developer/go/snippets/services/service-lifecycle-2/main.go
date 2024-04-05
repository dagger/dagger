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

	// set value
	setter, err := redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	// get value
	getter, err := redisCLI.
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	return setter + getter, nil
}
