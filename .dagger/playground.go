package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Creates a complete end-to-end build environment with CLI and engine for interactive testing
func (dev *DaggerDev) Playground(
	ctx context.Context,
	// Build from a custom base image
	// +optional
	base *dagger.Container,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
) (*dagger.Container, error) {
	if base == nil {
		base = dag.Wolfi().Container().WithEnvVariable("HOME", "/root")
	}
	base = base.WithWorkdir("$HOME", dagger.ContainerWithWorkdirOpts{Expand: true})
	svc := dag.DaggerEngine().Service("", dagger.DaggerEngineServiceOpts{
		GpuSupport:  gpuSupport,
		SharedCache: sharedCache,
	})
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return base.
			WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
			WithServiceBinding("dagger-engine", svc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint),
		nil
}
