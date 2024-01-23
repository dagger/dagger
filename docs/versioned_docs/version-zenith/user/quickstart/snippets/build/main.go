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
	return dag.Container().
		From("node:18-slim").
		WithDirectory("/src", dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "node_modules/"},
		})).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}

// create a production build
func (m *MyModule) Build() *Directory {
	return dag.Container().
		From("node:18-slim").
		WithDirectory("/src", dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "node_modules/"},
		})).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "build"}).
		Directory("./dist")
}
