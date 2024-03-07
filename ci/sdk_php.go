package main

import (
	"context"
	"fmt"
)

type PHPSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the PHP SDK
func (t PHPSDK) Lint(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Test tests the PHP SDK
func (t PHPSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the PHP SDK API
func (t PHPSDK) Generate(ctx context.Context) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}

// Publish publishes the PHP SDK
func (t PHPSDK) Publish(ctx context.Context, tag string) error {
	return fmt.Errorf("not implemented")
}

// Bump the PHP SDK's Engine dependency
func (t PHPSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}
