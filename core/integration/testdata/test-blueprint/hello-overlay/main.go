package main

import (
	"context"

	"dagger/hello-overlay/internal/dagger"
)

// HelloOverlay is the overlay that wraps the base hello toolchain
type HelloOverlay struct{}

// Message returns a wrapped message from the base hello toolchain
func (m *HelloOverlay) Message(ctx context.Context) (string, error) {
	// Access the base hello toolchain through dag
	msg, err := dag.Hello().Message(ctx)
	if err != nil {
		return "", err
	}
	return "overlay: " + msg, nil
}

// ConfigurableMessage returns a wrapped configurable message from the base hello toolchain
func (m *HelloOverlay) ConfigurableMessage(
	ctx context.Context,
	// +default="hello"
	message string,
) (string, error) {
	// Access the base hello toolchain through dag
	// The SDK generates an options struct for functions with optional parameters
	msg, err := dag.Hello().ConfigurableMessage(ctx, dagger.HelloConfigurableMessageOpts{
		Message: message,
	})
	if err != nil {
		return "", err
	}
	return "overlay says: " + msg, nil
}
