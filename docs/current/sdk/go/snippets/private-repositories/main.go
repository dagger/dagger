package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Retrieve path of authentication agent socket from host
	sshAgentPath := os.Getenv("SSH_AUTH_SOCK")

	// Private repository with a README.md file at the root.
	readme, err := client.
		Git("git@private-repository.git").
		Branch("main").
		Tree(
			dagger.GitRefTreeOpts{
				SSHAuthSocket: client.Host().UnixSocket(sshAgentPath),
			},
		).
		File("README.md").
		Contents(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println("readme", readme)
}
