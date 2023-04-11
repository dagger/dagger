package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	secretEnv := client.SetSecret("my-secret-var", "secret value here")
	secretFile := client.SetSecret("my-secret-file", "secret file content here")

	// dump secrets to console
	out, err := client.Container().
		From("alpine:3.17").
		WithSecretVariable("MY_SECRET_VAR", secretEnv).
		WithMountedSecret("/my_secret_file", secretFile).
		WithExec([]string{"sh", "-c", `echo -e "secret env data: $MY_SECRET_VAR || secret file data: "; cat /my_secret_file`}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
