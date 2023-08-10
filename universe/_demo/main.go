package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	DaggerClient().Environment().
		WithCommand_(PublishAll).
		WithCheck_(IntegTest).
		WithCheck_(UnitTest).
		WithShell_(DevShell).
		Serve()
}

func PublishAll(ctx dagger.Context, version string) (string, error) {
	// First, publish the server
	_, err := DaggerClient().Demoserver().Publish(ctx, version)
	if err != nil {
		return "", fmt.Errorf("failed to publish go server: %w", err)
	}

	/*
		// if that worked, publish the client app
		err := Dagger().MyPythonApp().Publish(ctx, version)
		if err != nil {
			return fmt.Errorf("failed to publish python app: %w", err)
		}
	*/
	return "", nil
}

func UnitTest(ctx dagger.Context) (*dagger.EnvironmentCheckResult, error) {
	// TODO: sugar to make this less annoying
	return DaggerClient().EnvironmentCheck().
		WithSubcheck(DaggerClient().Democlient().UnitTest()).
		WithSubcheck(DaggerClient().Demoserver().UnitTest()).
		WithSubcheck(DaggerClient().EnvironmentCheck().WithName("ctrtest").WithContainer(
			DaggerClient().Apko().Wolfi([]string{"coreutils"}).WithExec([]string{"false"}),
		)).
		Result(), nil
}

func IntegTest(ctx dagger.Context) (*dagger.EnvironmentCheckResult, error) {
	// TODO: clientApp := Dagger().Democlient().Build()
	clientApp := DaggerClient().Apko().Wolfi([]string{"curl"})

	// TODO: need combined stdout/stderr really badly now
	stdout, err := clientApp.
		WithServiceBinding("server", DaggerClient().Demoserver().Container()).
		WithExec([]string{"curl", "http://server:8081/hello"}).
		Stdout(ctx)
	// TODO: this is all boilerplatey, sugar to support other return types will fix
	if err != nil {
		return DaggerClient().EnvironmentCheckResult(false, err.Error()), nil
	}
	return DaggerClient().EnvironmentCheckResult(true, stdout), nil
}

func DevShell(ctx dagger.Context) (*dagger.Container, error) {
	// TODO: clientApp := Dagger().Democlient().Build()
	clientApp := DaggerClient().Apko().Wolfi([]string{"curl"})

	return clientApp.
		WithServiceBinding("server", DaggerClient().Demoserver().Container()).
		WithEntrypoint([]string{"sh"}), nil
}
