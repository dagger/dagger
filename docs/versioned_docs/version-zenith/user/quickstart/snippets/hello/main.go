package main

import (
	"context"
)

type MyModule struct{}

// say hello
func (m *MyModule) HelloFromDagger(ctx context.Context) (string, error) {
	version, err := dag.Container().From("node:18-slim").WithExec([]string{"node", "-v"}).Stdout(ctx)
	if err != nil {
		return "", err
	}
	return ("Hello from Dagger and Node " + version), nil
}
