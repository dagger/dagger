package main

import (
	"context"
	"fmt"
)

type TypescriptSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Typescript SDK
func (t TypescriptSDK) Lint(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Test tests the Typescript SDK
func (t TypescriptSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the Typescript SDK API
func (t TypescriptSDK) Generate(ctx context.Context) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}

// Publish publishes the Typescript SDK
func (t TypescriptSDK) Publish(ctx context.Context, tag string) error {
	return fmt.Errorf("not implemented")
}

// Bump the Typescript SDK's Engine dependency
func (t TypescriptSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}
