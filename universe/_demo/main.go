package main

import (
	"context"
	"fmt"
	"strings"
)

/*
* CI Platform Environment
 */

func main() {
	dag.Environment().
		WithCommand(PublishAll).
		WithCheck(UnitTest).
		WithArtifact(dag.DemoServer().Binary()).
		WithArtifact(dag.DemoServer().ServerImage()).
		WithArtifact(dag.DemoClient().ClientImage()).
		WithShell(Dev).
		// WithCheck(IntegTest).
		Serve()
}

func PublishAll(ctx context.Context, version string) (string, error) {
	// First, publish the server
	serverOutput, err := dag.DemoServer().Publish(ctx, version)
	if err != nil {
		return "", fmt.Errorf("failed to publish go server: %w", err)
	}

	// if that worked, publish the client app
	clientOutput, err := dag.DemoClient().Publish(ctx, version)
	if err != nil {
		return "", fmt.Errorf("failed to publish python app: %w", err)
	}

	return strings.Join([]string{serverOutput, clientOutput}, "\n"), nil
}

func UnitTest(ctx context.Context) (*EnvironmentCheckResult, error) {
	return dag.EnvironmentCheck().
		WithSubcheck(dag.DemoClient().UnitTest()).
		WithSubcheck(dag.DemoServer().UnitTest()).Result(), nil
}

func Dev(ctx context.Context) (*Container, error) {
	clientApp := dag.DemoClient().ClientImage().Container()

	return clientApp.
		WithServiceBinding("server", dag.DemoServer().ServerImage().Container()).
		WithEntrypoint([]string{"sh"}), nil
}

/*
func IntegTest(ctx context.Context) (*EnvironmentCheckResult, error) {
	clientApp := dag.DemoClient().ClientImage().Container()

	stdout, err := clientApp.
		WithServiceBinding("server", dag.DemoServer().ServerImage().Container()).
		WithExec(nil).
		Stdout(ctx)
	if err != nil {
		return dag.EnvironmentCheckResult().WithOutput(err.Error()), nil
	}
	return dag.EnvironmentCheckResult().WithSuccess(true).WithOutput(stdout), nil
}
*/
