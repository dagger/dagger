package main

import (
	"context"
)

type MyModule struct{}

// say hello
func (m *MyModule) HelloFromDagger(ctx context.Context) string {
	version, err := dag.Container().From("node:18-slim").WithExec([]string{"node", "-v"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}
	return ("Hello from Dagger and Node " + version)
}

// run unit tests
func (m *MyModule) Test(ctx context.Context) (string, error) {
	return dag.Node().
		WithVersion("18").
		WithNpm().
		WithSource(dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "**/node_modules"},
		})).
		Install(nil).
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}
