package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	Dagger().Environment().
		WithCheck_(IntegTest).
		WithExtension(Dagger().MyPythonApp(), "client").
		WithExtension(Dagger().MyGoServer(), "server").
		Serve()
}

func IntegTest(ctx dagger.Context) error {
	pythonApp := Dagger().MyPythonApp().Build()
	goServer := Dagger().MyGoServer().Build()

	return pythonApp.
		WithServiceBinding("server", goServer).
		WithExec([]string{"say-hello", "server"}).
		Sync(ctx)
}

func PublishAll(ctx dagger.Context, version string) error {
	// First, publish the server
	err := Dagger().MyGoServer().Publish(ctx, version)
	if err != nil {
		return fmt.Errorf("failed to publish go server: %w", err)
	}

	// if that worked, publish the client app
	err := Dagger().MyPythonApp().Publish(ctx, version)
	if err != nil {
		return fmt.Errorf("failed to publish python app: %w", err)
	}
	return nil
}

func DevShell(ctx dagger.Context) (*dagger.Container, error) {
	pythonApp := Dagger().MyPythonApp().Build()
	goServer := Dagger().MyGoServer().Build()

	return pythonApp.
		WithServiceBinding("server", goServer).
		WithEntrypoint([]string{"bash"})
}
