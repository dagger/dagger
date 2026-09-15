package main

import (
	"context"
	"strconv"
	"time"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Fn(ctx context.Context, sock *dagger.Socket, msg string) (string, error) {
	return dag.Container().
		From("alpine:3.20").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithEnvVariable("BUSTA", strconv.Itoa(int(time.Now().UnixNano()))).
		WithUnixSocket("/foo.sock", sock).
		WithExec([]string{"sh", "-c", "echo -n " + msg + " | nc -N -U /foo.sock"}).
		Stdout(ctx)
}
