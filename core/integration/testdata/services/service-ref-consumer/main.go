// A module whose constructor accepts optional core-type args (Service,
// Container), used to test wiring another module's function output in via
// workspace settings.
package main

import (
	"context"

	"dagger/service-ref-consumer/internal/dagger"
)

type ServiceRefConsumer struct {
	App  *dagger.Service
	Base *dagger.Container
}

func New(
	// +optional
	app *dagger.Service,
	// +optional
	base *dagger.Container,
) *ServiceRefConsumer {
	return &ServiceRefConsumer{App: app, Base: base}
}

// Returns "true" if a Service was provided, "false" otherwise.
func (m *ServiceRefConsumer) HasService() string {
	if m.App == nil {
		return "false"
	}
	return "true"
}

// Returns the PROVIDED_BY env var of the provided container, or "none" if no
// container was provided.
func (m *ServiceRefConsumer) ContainerProvidedBy(ctx context.Context) (string, error) {
	if m.Base == nil {
		return "none", nil
	}
	return m.Base.EnvVariable(ctx, "PROVIDED_BY")
}
