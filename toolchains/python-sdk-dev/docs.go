package main

import (
	"fmt"

	"dagger/python-sdk-dev/internal/dagger"
)

type Docs struct {
	// +private
	Container *dagger.Container
}

// Build the documentation
func (d *Docs) Build() *dagger.Directory {
	return d.Container.
		WithWorkdir("docs").
		WithExec(uvRun(
			"sphinx-build", "-v", ".", "/dist",
		)).
		Directory("/dist")
}

// Build and preview the documentation in the browser
func (d *Docs) Preview(
	// The port to bind the web preview for the built docs
	// +default=8000
	bind int,
) *dagger.Service {
	return d.Container.
		With(mountedWorkdir(d.Build())).
		WithExposedPort(bind).
		AsService(dagger.ContainerAsServiceOpts{Args: uvRun(
			"python", "-m", "http.server", fmt.Sprintf("%d", bind),
		)})
}
