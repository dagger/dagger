package main

import (
	"context"
	"strconv"
	"time"

	"dagger/caller/internal/dagger"
)

type Caller struct{}

func (m *Caller) Count(ctx context.Context, service *dagger.Service, buster string) (int, error) {
	hn, err := service.Hostname(ctx)
	if err != nil {
		return 0, err
	}

	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://" + hn}).
		Stdout(ctx)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(resp)
}
