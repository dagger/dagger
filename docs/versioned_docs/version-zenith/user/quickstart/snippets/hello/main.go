package main

import (
	"context"
)

type Mymodule struct{}

// say hello
func (m *Mymodule) Hello(ctx context.Context) (string, error) {
	version, err := dag.Container().From("node:18-slim").WithExec([]string{"node", "-v"}).Stdout(ctx)
	if err != nil {
		return "", err
	}
	return ("Hello from Dagger and Node " + version), nil
}
