package main

import (
	"fmt"
	"strings"

	"dagger.io/dagger"
)

func main() {
	DaggerClient().Environment().
		WithCommand_(PublishAll).
		WithCheck_(UnitTest).
		WithArtifact(DaggerClient().DemoServer().Binary()).
		WithArtifact(DaggerClient().DemoServer().ServerImage()).
		WithArtifact(DaggerClient().DemoClient().ClientImage()).
		WithShell_(Dev).
		// WithCheck_(IntegTest).
		Serve()
}

func PublishAll(ctx dagger.Context, version string) (string, error) {
	// First, publish the server
	serverOutput, err := DaggerClient().DemoServer().Publish(ctx, version)
	if err != nil {
		return "", fmt.Errorf("failed to publish go server: %w", err)
	}

	// if that worked, publish the client app
	clientOutput, err := DaggerClient().DemoClient().Publish(ctx, version)
	if err != nil {
		return "", fmt.Errorf("failed to publish python app: %w", err)
	}

	return strings.Join([]string{serverOutput, clientOutput}, "\n"), nil
}

func UnitTest(ctx dagger.Context) (*dagger.EnvironmentCheckResult, error) {
	return DaggerClient().EnvironmentCheck().
		WithSubcheck(DaggerClient().DemoClient().UnitTest()).
		WithSubcheck(DaggerClient().DemoServer().UnitTest()).Result(), nil
}

func Dev(ctx dagger.Context) (*dagger.Container, error) {
	clientApp := DaggerClient().DemoClient().ClientImage().Container()

	return clientApp.
		WithServiceBinding("server", DaggerClient().DemoServer().ServerImage().Container()).
		WithEntrypoint([]string{"sh"}), nil
}

/*
func IntegTest(ctx dagger.Context) (*dagger.EnvironmentCheckResult, error) {
	clientApp := DaggerClient().DemoClient().ClientImage().Container()

	stdout, err := clientApp.
		WithServiceBinding("server", DaggerClient().DemoServer().ServerImage().Container()).
		WithExec(nil).
		Stdout(ctx)
	if err != nil {
		return DaggerClient().EnvironmentCheckResult().WithOutput(err.Error()), nil
	}
	return DaggerClient().EnvironmentCheckResult().WithSuccess(true).WithOutput(stdout), nil
}
*/
