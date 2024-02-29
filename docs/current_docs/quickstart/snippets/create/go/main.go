package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type MyModule struct{}

func (m *MyModule) BuildAndPublish(ctx context.Context, buildSrc *Directory, buildArgs []string, outFile string) (string, error) {
	// build project and return binary file
	file := dag.
		Golang().
		WithProject(buildSrc).
		Build(buildArgs).File(outFile)

	// build and publish container with binary file
	return dag.
		Wolfi().
		Container().
		WithFile("/usr/local/bin/dagger", file).
		Publish(ctx, fmt.Sprintf("ttl.sh/my-dagger-container-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
}
