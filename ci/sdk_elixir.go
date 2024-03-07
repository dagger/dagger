package main

import (
	"context"
	"fmt"
)

type ElixirSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Elixir SDK
func (t ElixirSDK) Lint(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Test tests the Elixir SDK
func (t ElixirSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the Elixir SDK API
func (t ElixirSDK) Generate(ctx context.Context) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}

// Publish publishes the Elixir SDK
func (t ElixirSDK) Publish(ctx context.Context, tag string) error {
	return fmt.Errorf("not implemented")
}

// Bump the Elixir SDK's Engine dependency
func (t ElixirSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	return nil, fmt.Errorf("not implemented")
}
