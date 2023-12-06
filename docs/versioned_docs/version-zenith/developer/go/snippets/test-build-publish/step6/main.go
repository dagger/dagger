package main

import (
	"context"
)

type Mymod struct{}

const defaultNodeVersion = "16"

func (m *Mymod) buildBase(nodeVersion Optional[string]) *Node {
	return dag.Node().
		WithVersion(nodeVersion.GetOr(defaultNodeVersion)).
		WithNpm().
		WithSource(dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "**/node_modules"},
		})).
		Install(nil)
}

func (m *Mymod) Test(ctx context.Context, nodeVersion Optional[string]) (string, error) {
	return m.buildBase(nodeVersion).
		Run([]string{"test", "--", "--watchAll=false"}).
		Stderr(ctx)
}

func (m *Mymod) Build(nodeVersion Optional[string]) *Directory {
	return m.buildBase(nodeVersion).Build().Container().Directory("./build")
}

func (m *Mymod) Package(nodeVersion Optional[string]) *Container {
	return dag.Container().From("nginx:1.23-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build(nodeVersion)).
		WithExposedPort(80)
}

func (m *Mymod) PackageService(nodeVersion Optional[string]) *Service {
	return m.Package(nodeVersion).
		AsService()
}

func (m *Mymod) Publish(ctx context.Context, nodeVersion Optional[string]) (string, error) {
	return dag.Ttlsh().Publish(ctx, m.Package(nodeVersion))
}
