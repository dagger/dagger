package main

import (
	"context"
	"strconv"
	"time"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Fn(ctx context.Context, sock *dagger.Socket, msg string) (string, error) {
	dockerfile := "FROM alpine:3.20\n" +
		"RUN apk add netcat-openbsd\n" +
		"ARG MSG\n" +
		"ARG CACHEBUST\n" +
		"RUN --mount=type=ssh sh -c 'echo -n $MSG | nc -w1 -N -U $SSH_AUTH_SOCK > /result'\n"

	return dag.Directory().
		WithNewFile("Dockerfile", dockerfile).
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			SSH: sock,
			BuildArgs: []dagger.BuildArg{
				{Name: "MSG", Value: msg},
				{Name: "CACHEBUST", Value: strconv.Itoa(int(time.Now().UnixNano()))},
			},
		}).
		File("/result").
		Contents(ctx)
}
