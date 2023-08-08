package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	DaggerClient().Environment().
		WithCommand_(PublishAll).
		// TODO: WithCheck_(IntegTest).
		// TODO: WithShell_(DevShell).
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

/*
func IntegTest(ctx dagger.Context) error {
	// TODO: clientApp := Dagger().Democlient().Build()
	clientApp := DaggerClient().Apko().Wolfi([]string{"curl"})

	_, err := clientApp.
		WithServiceBinding("server", DaggerClient().Demoserver().Container()).
		WithExec([]string{"curl", "http://server/hello"}).
		Sync(ctx)
	return err
}

func DevShell(ctx dagger.Context) (*dagger.Container, error) {
	// TODO: clientApp := Dagger().Democlient().Build()
	clientApp := DaggerClient().Apko().Wolfi([]string{"curl"})

	return clientApp.
		WithServiceBinding("server", DaggerClient().Demoserver().Container()).
		WithEntrypoint([]string{"sh"}), nil
}
*/
