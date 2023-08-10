package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	dagger.DefaultClient().Environment().
		WithCheck_(UnitTest).
		WithCommand_(Publish).
		WithFunction_(Container). // TODO: should be an artifact
		Serve()
}

func buildBase(ctx dagger.Context) *dagger.Container {
	return dagger.DefaultClient().Apko().Wolfi([]string{"go-1.20"})
}

func binary(ctx dagger.Context) *dagger.File {
	return dagger.DefaultClient().Go().Build(
		buildBase(ctx),
		dagger.DefaultClient().Host().Directory("."),
		dagger.GoBuildOpts{
			Packages: []string{"./universe/_demo/server/cmd/server"},
		},
	).File("server")
}

func UnitTest(ctx dagger.Context) *dagger.EnvironmentCheckResult {
	return dagger.DefaultClient().Go().Test(
		buildBase(ctx),
		dagger.DefaultClient().Host().Directory("."),
		dagger.GoTestOpts{
			Packages: []string{"./universe/_demo/server/cmd/server"},
		},
	)
}

func Publish(ctx dagger.Context, version string) (string, error) {
	if version == "fail" {
		fmt.Println("OH NO! Publishing failed!")
		return "", fmt.Errorf("publish failed")
	}

	// TODO: call go releaser from universe?
	fmt.Println("Publishing version", version)
	return "", nil
}

func Container(ctx dagger.Context) (*dagger.Container, error) {
	return dagger.DefaultClient().Apko().Wolfi(nil).
		WithMountedFile("/usr/bin/server", binary(ctx)).
		WithExposedPort(8081).
		WithExec([]string{"/usr/bin/server"}), nil
}
