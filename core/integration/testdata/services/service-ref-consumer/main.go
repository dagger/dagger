// A module whose constructor accepts an optional Service, used to test
// wiring another module's +up service in via workspace settings.
package main

import (
	"dagger/service-ref-consumer/internal/dagger"
)

type ServiceRefConsumer struct {
	App *dagger.Service
}

func New(
	// +optional
	app *dagger.Service,
) *ServiceRefConsumer {
	return &ServiceRefConsumer{App: app}
}

// Returns "true" if a Service was provided, "false" otherwise.
func (m *ServiceRefConsumer) HasService() string {
	if m.App == nil {
		return "false"
	}
	return "true"
}
