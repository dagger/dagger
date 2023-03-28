package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// set a host environment variable
	const hostEnvName = "MY_SECRET_VAR"
	err := os.Setenv(hostEnvName, "secret value here")
	if err != nil {
		panic(err)
	}

	// create a test host file
	const hostFileContent = "secret file content here"
	err = os.WriteFile("my_secret_file", []byte(hostFileContent), 0o644)
	if err != nil {
		panic(err)
	}

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// load secrets
	secretEnv := client.Host().EnvVariable("MY_SECRET_VAR").Secret()
	secretFile := client.Host().Directory(".").File("my_secret_file").Secret()

	// dump secrets to console
	output, err := client.Container().
		From("alpine:3.17").
		WithSecretVariable("MY_SECRET_VAR", secretEnv).
		WithMountedSecret("/my_secret_file", secretFile).
		WithExec([]string{"sh", "-c", `echo -e "secret env data: $MY_SECRET_VAR || secret file data: "; cat /my_secret_file`}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(output)
}
