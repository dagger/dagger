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
		WithServiceBinding("redis-srv", redisSrv)

	// send ping from client to server
	return redisCLI.
		WithExec([]string{"redis-cli", "-h", "redis-srv", "ping"}).
		Stdout(ctx)
}
