package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
)

// LoadToDocker loads the engine container into docker
func (e *DaggerEngine) LoadToDocker(
	ctx context.Context,

	docker *dagger.Socket,
	name string,

	// +optional
	platform dagger.Platform,

	// Set target distro
	// +optional
	image *Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
) (*LoadedEngine, error) {
	ctr, err := e.Container(ctx, platform, image, gpuSupport)
	if err != nil {
		return nil, err
	}
	tar := ctr.AsTarball(dagger.ContainerAsTarballOpts{
		// use gzip to avoid incompatibility w/ older docker versions
		ForcedCompression: dagger.ImageLayerCompressionGzip,
	})

	loader := dag.Container().
		From("docker:cli").
		WithUnixSocket("/var/run/docker.sock", docker).
		WithMountedFile("/image.tar.gz", tar).
		WithEnvVariable("CACHEBUSTER", identity.NewID())

	stdout, err := loader.
		WithExec([]string{"docker", "load", "-i", "/image.tar.gz"}).
		Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker load failed: %w", err)
	}

	_, imageID, ok := strings.Cut(stdout, "Loaded image ID: sha256:")
	if !ok {
		_, imageID, ok = strings.Cut(stdout, "Loaded image: sha256:") // podman
		if !ok {
			return nil, fmt.Errorf("unexpected output from docker load")
		}
	}
	imageID = strings.TrimSpace(imageID)

	_, err = loader.
		WithExec([]string{"docker", "tag", imageID, name}).
		Sync(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker tag failed: %w", err)
	}

	return &LoadedEngine{
		Loader:     loader,
		Image:      name,
		GPUSupport: gpuSupport,
	}, nil
}

type LoadedEngine struct {
	Loader *dagger.Container // +private
	Image  string

	GPUSupport bool // +private
}

// Start the loaded engine container
func (e LoadedEngine) Start(
	ctx context.Context,

	// +optional
	// +default="dagger-engine.dev"
	name string,
	// +optional
	cloudToken *dagger.Secret,
) error {
	loader := e.Loader

	_, err := loader.WithExec([]string{"docker", "rm", "-fv", name}).Sync(ctx)
	if err != nil {
		return err
	}

	args := []string{
		"docker",
		"run",
		"-d",
	}
	if e.GPUSupport {
		args = append(args, "--gpus", "all")
		loader = loader.WithEnvVariable("_EXPERIMENTAL_DAGGER_GPU_SUPPORT", "true")
	}
	if cloudToken != nil {
		// NOTE: this is only for connecting to dagger cloud's cache service
		args = append(args, "-e", "DAGGER_CLOUD_TOKEN")
		loader = loader.WithSecretVariable("DAGGER_CLOUD_TOKEN", cloudToken)
	}
	args = append(args, []string{
		"-v", name + ":" + distconsts.EngineDefaultStateDir,
		"--name", name,
		"--privileged",
	}...)
	args = append(args, e.Image, "--extra-debug", "--debugaddr=0.0.0.0:6060")

	_, err = loader.WithExec(args).Sync(ctx)
	return err
}
