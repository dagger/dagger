package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"

	"dagger.io/dagger"
)

func main() {
	// initialize Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// read file
	f, err := ioutil.ReadFile("/home/USER/.config/gh/hosts.yml")
	if err != nil {
		panic(err)
	}

	// set secret to file contents
	secret := client.SetSecret("ghConfig", string(f))

	// mount secret as file in container
	out, err := client.
		Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "github-cli"}).
		WithMountedSecret("/root/.config/gh/hosts.yml", secret).
		WithWorkdir("/root").
		WithExec([]string{"gh", "auth", "status"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
