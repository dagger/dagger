package main

import (
	"context"
)

type Mymodule struct{}

func (m *Mymodule) Hello(ctx context.Context) string {
	version, err := dag.Container().From("node:18-slim").WithExec([]string{"node", "-v"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}
	return ("Hello from Dagger and Node " + version)
}

func (m *Mymodule) Test(ctx context.Context) (string, error) {
	return m.buildBaseImage().
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}

func (m *Mymodule) Build() *Directory {
	return m.buildBaseImage().
		Build().
		Container().
		Directory("./dist")
}

func (m *Mymodule) Package() *Container {
	return dag.Container().From("nginx:1.23-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build()).
		WithExposedPort(80)
}

func (m *Mymodule) Publish(ctx context.Context) (string, error) {
	return dag.Ttlsh().Publish(ctx, m.Package())
}

func (m *Mymodule) buildBaseImage() *Node {
	return dag.Node().
		WithVersion("18").
		WithNpm().
		WithSource(dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "**/node_modules"},
		})).
		Install(nil)
}
