package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	dagger.DefaultContext().Client().Environment().
		WithCheck_(UnitTest).
		Serve()
}

func Build(ctx dagger.Context) (*dagger.Container, error) {
	panic("implement me")
}

func UnitTest(ctx dagger.Context) error {
	return ctx.Client().Go().Test(ctx.Client().Host().Directory("."), "./...")
}

func Publish(ctx dagger.Context, version string) error {
	// TODO: call go releaser from universe?
	fmt.Println("Publishing version", version)
	return nil
}
