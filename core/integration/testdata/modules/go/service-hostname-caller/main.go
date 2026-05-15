package main

import (
	"context"
	"fmt"
	"time"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) error {
	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup1"}).
		Stdout(ctx)
	if err == nil {
		return fmt.Errorf("should not have been able to reach service")
	}

	srv := dag.Container().
		From("busybox:1.37.0").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am within the called module.").
		WithDefaultArgs([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup1")

	_, err = srv.Start(ctx)
	if err != nil {
		return err
	}

	resp, err = dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup1"}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach service: %w", err)
	}
	if resp != "I am within the called module." {
		return fmt.Errorf("unexpected response: %q", resp)
	}
	return nil
}
