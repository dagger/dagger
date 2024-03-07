package main

import (
	"context"
	"fmt"
)

type JavaSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Java SDK
func (t JavaSDK) Lint(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Test tests the Java SDK
func (t JavaSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the Java SDK API
func (t JavaSDK) Generate(ctx context.Context) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}

// Publish publishes the Java SDK
func (t JavaSDK) Publish(ctx context.Context, tag string) error {
	return fmt.Errorf("not implemented")
}

// Bump the Java SDK's Engine dependency
func (t JavaSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}
