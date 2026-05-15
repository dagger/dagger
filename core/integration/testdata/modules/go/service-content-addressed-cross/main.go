package main

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"
)

type Hoster struct{}

//go:embed counter/main.go
var counterMain string

func (m *Hoster) Run(ctx context.Context) error {
	counter := dag.Container().
		From("golang:1.26-alpine").
		WithWorkdir("/srv").
		WithNewFile("main.go", counterMain).
		WithDefaultArgs([]string{"go", "run", "main.go"}).
		WithExposedPort(80).
		AsService()

	// explicitly start since we want to test that it's the same instance
	// across the following call and subsequent cross-module calls
	_, err := counter.Start(ctx)
	if err != nil {
		return err
	}

	// first query the service locally, to ensure the subsequent calls
	// start at 2
	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithServiceBinding("counter", counter).
		WithExec([]string{"wget", "-O-", "http://counter"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(resp)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("expected %d, got %d", 1, n)
	}

	n, err = dag.Caller().Count(ctx, counter, time.Now().String())
	if err != nil {
		return err
	}
	if n != 2 {
		return fmt.Errorf("expected %d, got %d", 2, n)
	}

	n, err = dag.Caller().Count(ctx, counter, time.Now().String())
	if err != nil {
		return err
	}
	if n != 3 {
		return fmt.Errorf("expected %d, got %d", 3, n)
	}

	return nil
}
