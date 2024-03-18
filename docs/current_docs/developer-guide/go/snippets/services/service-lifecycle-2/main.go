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
		AsService()

	// create Redis client container
	redisCLI := dag.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	// set value
	setter, err1 := redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		Stdout(ctx)

	if err1 != nil {
		return "", err1
	}

	// get value
	getter, err2 := redisCLI.
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)

	if err2 != nil {
		return "", err2
	}

	return setter + getter, nil
}
