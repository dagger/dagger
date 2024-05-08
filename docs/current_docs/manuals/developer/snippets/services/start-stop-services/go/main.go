package main

import (
	"context"
)

type MyModule struct{}

// explicitly starts and stops Redis service
func (m *MyModule) RedisService(ctx context.Context) (string, error) {
	redisSrv := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService()

	// start Redis ahead of time so it stays up for the duration of the test
	redisSrv, err := redisSrv.Start(ctx)
	if err != nil {
		return "", err
	}

	// stop the service when done
	defer redisSrv.Stop(ctx)

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
