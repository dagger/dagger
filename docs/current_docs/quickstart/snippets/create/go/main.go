package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type Example struct{}

func (m *Example) BuildAndPublish(ctx context.Context, buildSrc *Directory, buildArgs []string) (string, error) {
	// retrieve a new Wolfi container
	ctr := dag.Wolfi().Container()

	// publish the Wolfi container with the build result
	return dag.
		Golang().
		BuildContainer(GolangBuildContainerOpts{Source: buildSrc, Args: buildArgs, Base: ctr}).
		Publish(ctx, fmt.Sprintf("ttl.sh/my-hello-container-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
}
