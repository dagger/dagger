package main

import (
	"context"
)

type MyModule struct{}

// Build and publish multi-platform image
func (m *MyModule) Build(
	ctx context.Context,
	// Source code location
	// can be local directory or remote Git repository
	src *Directory,
) (string, error) {
	// platforms to build for and push in a multi-platform image
	var platforms = []Platform{
		"linux/amd64", // a.k.a. x86_64
		"linux/arm64", // a.k.a. aarch64
		"linux/s390x", // a.k.a. IBM S/390
	}

	// container registry for the multi-platform image
	const imageRepo = "ttl.sh/myapp:latest"

	platformVariants := make([]*Container, 0, len(platforms))
	for _, platform := range platforms {
		// parse architecture using containerd utility module
		platformArch, err := dag.Containerd().ArchitectureOf(ctx, platform)

		if err != nil {
			return "", err
		}

		// pull golang image for the *host* platform, this is done by
		// not specifying the a platform. The default is the host platform.
		ctr := dag.Container().
			From("golang:1.21-alpine").
			// mount source code
			WithDirectory("/src", src).
			// mount empty dir where built binary will live
			WithDirectory("/output", dag.Directory()).
			// ensure binary will be statically linked and thus executable
			// in the final image
			WithEnvVariable("CGO_ENABLED", "0").
			// configure go compiler to use cross-compilation targeting the
			// desired platform
			WithEnvVariable("GOOS", "linux").
			WithEnvVariable("GOARCH", platformArch).
			// build binary and put result at mounted output directory
			WithWorkdir("/src").
			WithExec([]string{"go", "build", "-o", "/output/hello"})

		// select output directory
		outputDir := ctr.Directory("/output")

		// wrap the output directory in the new empty container marked
		// with the same platform
		binaryCtr := dag.Container(ContainerOpts{Platform: platform}).
			WithRootfs(outputDir)

		platformVariants = append(platformVariants, binaryCtr)
	}

	// publish to registry
	imageDigest, err := dag.Container().
		Publish(ctx, imageRepo, ContainerPublishOpts{
			PlatformVariants: platformVariants,
		})

	if err != nil {
		return "", err
	}

	// return build directory
	return imageDigest, nil
}
