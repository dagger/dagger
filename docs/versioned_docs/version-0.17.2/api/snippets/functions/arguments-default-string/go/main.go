package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Hello(
	ctx context.Context,
	// +default="world"
	name string,
) (string, error) {
	return fmt.Sprintf("Hello, %s", name), nil
}
