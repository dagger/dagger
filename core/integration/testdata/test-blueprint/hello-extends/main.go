package main

import (
	"context"

	"dagger/hello-extends/internal/dagger"
)

// HelloExtends extends the base hello toolchain
type HelloExtends struct{}

// Message returns a wrapped message from the base hello toolchain
func (m *HelloExtends) Message(ctx context.Context) (string, error) {
	// Access the base hello toolchain through dag
	msg, err := dag.Hello().Message(ctx)
	if err != nil {
		return "", err
	}
	return "extended: " + msg, nil
}

// ConfigurableMessage returns a wrapped configurable message from the base hello toolchain
func (m *HelloExtends) ConfigurableMessage(
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
	return "extended says: " + msg, nil
}
