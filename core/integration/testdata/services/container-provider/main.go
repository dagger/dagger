// A module providing a Container-returning function, used to test wiring a
// container into another module's constructor via workspace settings.
package main

import "dagger/container-provider/internal/dagger"

type ContainerProvider struct{}

// Returns a base container for consumers
func (m *ContainerProvider) Image() *dagger.Container {
	return dag.Container().
		From("alpine:3.22.1").
		WithEnvVariable("PROVIDED_BY", "container-provider")
}
