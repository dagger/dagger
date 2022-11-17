package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// pull a Windows base image
	ctr := client.
		Container(dagger.ContainerOpts{Platform: "windows/amd64"}).
		From("mcr.microsoft.com/windows/nanoserver:ltsc2022")

	// listing files works, no error should be returned
	entries, err := ctr.Rootfs().Entries(ctx)
	if err != nil {
		panic(err) // shouldn't happen
	}
	for _, entry := range entries {
		fmt.Println(entry)
	}

	// however, executing a command will fail
	_, err = ctr.Exec(dagger.ContainerExecOpts{
		Args: []string{"cmd.exe"},
	}).Stdout().Contents(ctx)
	if err != nil {
		panic(err) // should happen
	}
}
