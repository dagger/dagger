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

	// send ping from client to server
	ping := redisCLI.WithExec([]string{"ping"})
	return ping.Stdout(ctx)
}
