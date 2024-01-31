package main

import (
	"context"
)

type MyMod struct{}

const defaultNodeVersion = "16"

func (m *MyMod) buildBase(nodeVersion string) *Node {
	if nodeVersion == "" {
		nodeVersion = defaultNodeVersion
	}
	return dag.Node().
		WithVersion(nodeVersion.GetOr(defaultNodeVersion)).
		WithNpm().
		WithSource(dag.CurrentModule().Source()).
		Install(nil)
}

func (m *MyMod) Test(
	ctx context.Context,
	// +optional
	nodeVersion string,
) (string, error) {
	return m.buildBase(nodeVersion).
		Run([]string{"test", "--", "--watchAll=false"}).
		Stderr(ctx)
}

func (m *MyMod) Build(
	// +optional
	nodeVersion string,
) *Directory {
	return m.buildBase(nodeVersion).Build().Container().Directory("./build")
}

func (m *MyMod) Package(
	// +optional
	nodeVersion string,
) *Container {
	return dag.Container().From("nginx:1.23-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build(nodeVersion)).
		WithExposedPort(80)
}

func (m *MyMod) PackageService(
	// +optional
	nodeVersion string,
) *Service {
	return m.Package(nodeVersion).
		AsService()
}

func (m *MyMod) Publish(
	ctx context.Context,
	// +optional
	nodeVersion string,
) (string, error) {
	return dag.Ttlsh().Publish(ctx, m.Package(nodeVersion))
}
