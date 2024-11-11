package main

import (
	"context"
	"main/internal/dagger"
)

type MyModule struct{}

// Run two services which are dependent on each other
func (m *MyModule) Services(ctx context.Context) (*dagger.Service, error) {

	svcA := dag.Container().From("nginx").
		WithExposedPort(80).
		WithExec([]string{"sh", "-c", `
	nginx & while true; do curl svcb:80 && sleep 1; done
			`}).
		AsService().WithHostname("svca")

	_, err := svcA.Start(ctx)
	if err != nil {
		return nil, err
	}

	svcB := dag.Container().From("nginx").
		WithExposedPort(80).
		WithExec([]string{"sh", "-c", `
nginx & while true; do curl svca:80 && sleep 1; done
			`}).
		AsService().WithHostname("svcb")

	svcB, err = svcB.Start(ctx)
	if err != nil {
		return nil, err
	}

	return svcB, nil
}
