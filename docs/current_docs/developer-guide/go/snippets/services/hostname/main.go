package main

import (
	"context"
)

type MyModule struct{}

// starts and returns the hostname of an HTTP service
func (m *MyModule) HttpService(ctx context.Context) (string, error) {
	val, err := dag.Container().
		From("python").
		WithExec([]string{"python", "-m", "http.server"}).
		AsService().
		Hostname(ctx)
	if err != nil {
		return "", err
	}
	return val, nil
}
