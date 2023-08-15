package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	dagger.DefaultClient().Environment().
		WithCheck_(UnitTest).
		WithArtifact_(ServerImage).
		WithArtifact_(Binary).
		WithCommand_(Publish).
		Serve()
}

func buildBase(ctx dagger.Context) *dagger.Container {
	return dagger.DefaultClient().Apko().Wolfi([]string{"go-1.20"})
}

// Unit tests for the server.
func UnitTest(ctx dagger.Context) *dagger.EnvironmentCheck {
	return dagger.DefaultClient().Go().Test(
		buildBase(ctx),
		dagger.DefaultClient().Host().Directory("."),
		dagger.GoTestOpts{
			Packages: []string{"./universe/_demo/server/cmd/server"},
			Verbose:  true,
		},
	)
}

// The server's binary as a file.
func Binary(ctx dagger.Context) *dagger.File {
	return dagger.DefaultClient().Go().Build(
		buildBase(ctx),
		dagger.DefaultClient().Host().Directory("."),
		dagger.GoBuildOpts{
			Static:   true,
			Packages: []string{"./universe/_demo/server/cmd/server"},
		},
	).File("server")
}

// The server container image.
func ServerImage(ctx dagger.Context) *dagger.Container {
	return dagger.DefaultClient().Apko().Wolfi(nil).
		WithMountedFile("/usr/bin/server", Binary(ctx)).
		WithExposedPort(8081).
		WithDefaultArgs(dagger.ContainerWithDefaultArgsOpts{
			Args: []string{"/usr/bin/server"},
		})
}

// Publish the server container image.
func Publish(ctx dagger.Context, version string) (string, error) {
	if version == "fail" {
		return "OH NO! Publishing failed!", fmt.Errorf("publish failed")
	}

	return "Published server version " + version, nil
}
