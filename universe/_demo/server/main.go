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

func Container(ctx dagger.Context) (*dagger.Container, error) {
	return dagger.DefaultClient().Apko().Wolfi(nil).
		WithMountedFile("/usr/bin/server", binary(ctx)).
		WithExposedPort(8080), nil
}

func binary(ctx dagger.Context) *dagger.File {
	srcDir := dagger.DefaultClient().Host().Directory(".")
	return buildBase(ctx).
		WithMountedDirectory("/src", srcDir).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/server", "./universe/_demo/server/cmd/server"}).
		File("/server")
}

func UnitTest(ctx dagger.Context) error {
	srcDir := dagger.DefaultClient().Host().Directory(".")

	// TODO: fix go universe env, use that
	_, err := buildBase(ctx).
		WithMountedDirectory("/src", srcDir).
		WithWorkdir("/src").
		WithExec([]string{"go", "test", "./universe/_demo/server/cmd/server"}).
		Sync(ctx)
	return err
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
