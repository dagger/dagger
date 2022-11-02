package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

// list of platforms to execute on
var platforms = []dagger.Platform{
	"linux/amd64", // a.k.a. x86_64
	"linux/arm64", // a.k.a. aarch64
	"linux/s390x", // a.k.a. IBM S/390
}

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	for _, platform := range platforms {
		// initialize this container with the platform
		ctr := client.Container(dagger.ContainerOpts{Platform: platform})

		// this alpine image has published versions for each of the
		// platforms above. If it was missing a platform,
		// an error would occur when executing a command below.
		ctr = ctr.From("alpine:3.16")

		// execute `uname -m`, which prints the current CPU architecture
		// being executed as
		stdout, err := ctr.
			Exec(dagger.ContainerExecOpts{
				Args: []string{"uname", "-m"},
			}).
			Stdout().Contents(ctx)
		if err != nil {
			panic(err)
		}

		// this should print 3 times, once for each of the architectures
		// being executed on
		fmt.Printf("I'm executing on architecture: %s\n", stdout)
	}
}
