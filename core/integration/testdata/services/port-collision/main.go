// A module with two services that expose the same port, used to test port collision detection.
package main

import (
	"dagger/port-collision/internal/dagger"
)

type PortCollision struct{}

// First web server on port 8080
// +up
func (m *PortCollision) Web() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(8080).
		AsService()
}

// Second web server also on port 8080
// +up
func (m *PortCollision) WebDuplicate() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(8080).
		AsService()
}
