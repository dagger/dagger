// A module with two +up services where one depends on the other via
// withServiceBinding. Tests that dagql deduplication ensures the shared
// service runs only once even though it's referenced both as a standalone
// +up service and as a binding inside another +up service.
package main

import (
	"dagger/service-binding/internal/dagger"
)

type ServiceBinding struct{}

// A simple backend service referenced by the frontend.
// +up
func (m *ServiceBinding) Backend() *dagger.Service {
	return dag.Container().
		From("redis:alpine").
		WithExposedPort(6379).
		AsService()
}

// A frontend that depends on backend via withServiceBinding.
// +up
func (m *ServiceBinding) Frontend() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(80).
		WithServiceBinding("backend", m.Backend()).
		AsService()
}
