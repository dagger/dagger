package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	// initialize Dagger client
	ctx := context.Background()

	if os.Getenv("GH_SECRET") == "" {
		panic("Environment variable GH_SECRET is not set")
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// read secret from host variable
	secret := client.SetSecret("gh-secret", os.Getenv("GH_SECRET"))

	// use secret in container environment
	out, err := client.
		Container().
		From("alpine:3.17").
		WithSecretVariable("GITHUB_API_TOKEN", secret).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
