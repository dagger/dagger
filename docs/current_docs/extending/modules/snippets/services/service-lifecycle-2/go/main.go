package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// creates Redis service and client
func (m *MyModule) RedisService(ctx context.Context) (string, error) {
	redisSrv := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	// create Redis client container
	redisCLI := dag.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv)

	args := []string{"redis-cli", "-h", "redis-srv"}

	// set value
	setter, err := redisCLI.
		WithExec(append(args, "set", "foo", "abc")).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	// get value
	getter, err := redisCLI.
		WithExec(append(args, "get", "foo")).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	return setter + getter, nil
}
