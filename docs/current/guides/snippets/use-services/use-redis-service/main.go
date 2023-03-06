package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	if len(os.Args) < 3 {
		fatal(fmt.Errorf("usage: %s <cache-key> <command ...>", os.Args[0]))
		return
	}

	key := os.Args[1]
	cmd := os.Args[2:]

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fatal(err)
		return
	}

	defer client.Close()

	redis := client.Container().From("redis")

	// create a Redis service with a persistent cache
	redisSrv := redis.
		WithExposedPort(6379).
		WithMountedCache("/data", client.CacheVolume(key)).
		WithWorkdir("/data").
		WithExec(nil)

	// create a redis-cli container that runs against the service
	redisCLI := redis.
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	// create the execution plan for the user's command
	// avoid caching via an environment variable
	redisCmd := redisCLI.
		WithEnvVariable("AT", time.Now().String()).
		WithExec(cmd)

	// first: run the command and immediately save
	_, err = redisCmd.WithExec([]string{"save"}).ExitCode(ctx)
	if err != nil {
		fatal(err)
		return
	}

	// then: print the output of the (cached) command
	out, err := redisCmd.Stdout(ctx)
	if err != nil {
		fatal(err)
		return
	}

	fmt.Print(out)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %s\n", err)
	os.Exit(1)
}
