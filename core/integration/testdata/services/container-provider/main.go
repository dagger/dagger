// A module providing a Container-returning function, used to test wiring a
// container into another module's constructor via workspace settings.
package main

import "dagger/container-provider/internal/dagger"

type ContainerProvider struct {
	Base *dagger.Container
}

func New(
	// +optional
	base *dagger.Container,
) *ContainerProvider {
	return &ContainerProvider{Base: base}
}

// Returns a base container for consumers
func (m *ContainerProvider) Image() *dagger.Container {
	base := m.Base
	if base == nil {
		base = dag.Container().From("alpine:3.22.1")
	}
	return base.WithEnvVariable("PROVIDED_BY", "container-provider")
}
