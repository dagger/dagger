package main

import (
	"context"
	"fmt"
)

type PythonSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Python SDK
func (t PythonSDK) Lint(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Test tests the Python SDK
func (t PythonSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the Python SDK API
func (t PythonSDK) Generate(ctx context.Context) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}

// Publish publishes the Python SDK
func (t PythonSDK) Publish(ctx context.Context, tag string) error {
	return fmt.Errorf("not implemented")
}

// Bump the Python SDK's Engine dependency
func (t PythonSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}
