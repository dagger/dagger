// The dumb-init binary which is part of the Dagger engine build
// See github.com/yelp/dumb-init
package main

import (
	"dagger/dumb-init/internal/dagger"

	"github.com/dagger/dagger/engine/distconsts"
)

func New(
	// Version of dumb-init to build
	// +optional
	// +default="v1.2.5"
	version string,
	// Platform to build for
	// +optional
	platform dagger.Platform,
) DumbInit {
	// FIXME: default value is broken for platform
	return DumbInit{
		Version:  version,
		Platform: platform,
	}
}

// Build the dumb-init binary
type DumbInit struct {
	Version  string
	Platform dagger.Platform
}

// Build the dumb-init binary
func (dumbInit DumbInit) Binary() *dagger.File {
	// FIXME: use wolfi module
	// dumb init is static, so we can use it on any base image
	return dag.
		Container(dagger.ContainerOpts{Platform: dumbInit.Platform}).
		From(distconsts.AlpineImage).
		WithExec([]string{"apk", "add", "build-base", "bash"}).
		WithMountedDirectory("/src", dag.Git("github.com/yelp/dumb-init").Ref(dumbInit.Version).Tree()).
		WithWorkdir("/src").
		WithExec([]string{"make"}).
		File("dumb-init")
}
