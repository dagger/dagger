package main

import (
	"context"
	"fmt"
)

// Server Team Environment
func main() {
	dag.Environment().
		WithCheck(UnitTest).
		WithArtifact(ServerImage).
		WithArtifact(Binary).
		WithCommand(Publish).
		Serve()
}

func buildBase(ctx context.Context) *Container {
	return dag.Apko().Wolfi([]string{"go-1.20"})
}

// Unit tests for the server.
func UnitTest(ctx context.Context) *EnvironmentCheck {
	return dag.Go().Test(
		buildBase(ctx),
		dag.Host().Directory("."),
		GoTestOpts{
			Packages: []string{"./universe/_demo/server/cmd/server"},
			Verbose:  true,
		},
	)
}

// The server's binary as a file.
func Binary(ctx context.Context) *File {
	return dag.Go().Build(
		buildBase(ctx),
		dag.Host().Directory("."),
		GoBuildOpts{
			Static:   true,
			Packages: []string{"./universe/_demo/server/cmd/server"},
		},
	).File("server")
}

// The server container image.
func ServerImage(ctx context.Context) *Container {
	return dag.Apko().Wolfi(nil).
		WithMountedFile("/usr/bin/server", Binary(ctx)).
		WithExposedPort(8081).
		WithDefaultArgs(ContainerWithDefaultArgsOpts{
			Args: []string{"/usr/bin/server"},
		})
}

// Publish the server container image.
func Publish(ctx context.Context, version string) (string, error) {
	if version == "fail" {
		return "OH NO! Publishing failed!", fmt.Errorf("publish failed")
	}

	return "Published server version " + version, nil
}
