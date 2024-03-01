package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type Example struct{}

// Build and publish a project using a Wolfi container
func (m *Example) BuildAndPublish(ctx context.Context, buildSrc *Directory, buildArgs []string) (string, error) {
	ctr := dag.Wolfi().Container()

	return dag.
		Golang().
		BuildContainer(GolangBuildContainerOpts{Source: buildSrc, Args: buildArgs, Base: ctr}).
		Publish(ctx, fmt.Sprintf("ttl.sh/my-hello-container-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
}
