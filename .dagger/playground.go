package main

import (
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Creates a complete end-to-end build environment with CLI and engine for interactive testing
func (dev *DaggerDev) Playground(
	// Build from a custom base image
	// +optional
	base *dagger.Container,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
) *dagger.Container {
	return dag.DaggerEngine().Playground(dagger.DaggerEnginePlaygroundOpts{
		Base:        base,
		GpuSupport:  gpuSupport,
		SharedCache: sharedCache,
	})
}
