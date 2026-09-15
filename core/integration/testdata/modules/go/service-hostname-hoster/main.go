package main

import (
	"context"
	"fmt"
)

type Hoster struct{}

func (m *Hoster) Run(ctx context.Context) error {
	srv := dag.Container().
		From("busybox:1.37.0").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am the one who hosts.").
		WithDefaultArgs([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup0")

	_, err := srv.Start(ctx)
	if err != nil {
		return err
	}

	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithExec([]string{"wget", "-O-", "http://wwwhatsup0"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if resp != "I am the one who hosts." {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
