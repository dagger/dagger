package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/dagger/dagger/cmd/engine/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

// LoadToDocker loads the engine container into docker
// +cache="session"
func (e *DaggerEngine) LoadToDocker(
	ctx context.Context,

	docker *dagger.Socket,

	// +optional
	// +default="localhost/dagger-engine.dev:latest"
	name string,

	// +optional
	platform dagger.Platform,

	// Set target distro
	// +default="alpine"
	image Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
) (*LoadedEngine, error) {
	ctr, err := e.Container(ctx, platform, image, gpuSupport, "", "")
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
		WithEnvVariable("CACHEBUSTER", rand.Text())

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
// +cache="session"
func (e LoadedEngine) Start(
	ctx context.Context,

	// +optional
	// +default="dagger-engine.dev"
	name string,
	// +optional
	cloudToken *dagger.Secret,
	// +optional
	cloudURL string,

	// +optional
	debug bool,

	// +optional
	extraHosts []string,
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

	// NOTE: this is only for connecting to dagger cloud's cache service
	if cloudToken != nil {
		args = append(args, "-e", "DAGGER_CLOUD_TOKEN")
		loader = loader.WithSecretVariable("DAGGER_CLOUD_TOKEN", cloudToken)
	}
	if cloudURL != "" {
		args = append(args, "-e", "DAGGER_CLOUD_URL")
		loader = loader.WithEnvVariable("DAGGER_CLOUD_URL", cloudURL)
	}
	if debug {
		args = append(args, "-p", "6060:6060")
	}

	if len(extraHosts) > 0 {
		args = append(args, "--add-host", strings.Join(extraHosts, ","))
	}

	args = append(args, []string{
		"-v", name + ":" + distconsts.EngineDefaultStateDir,
		"--name", name,
		// allow use of FUSE inside the engine container so features like
		// SSHFS volume mounts work. We add the device and relax apparmor.
		"--device", "/dev/fuse",
		"--security-opt", "apparmor:unconfined",
		// some setups require SYS_ADMIN to mount fuse
		"--cap-add", "SYS_ADMIN",
		"--privileged",
	}...)
	args = append(args, e.Image)
	args = append(args, "--extra-debug", "--debugaddr=0.0.0.0:6060")

	_, err = loader.WithExec(args).Sync(ctx)
	return err
}
