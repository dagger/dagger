package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Hello(
	ctx context.Context,
	// +optional
	name string,
) (string, error) {
	if name != "" {
		return fmt.Sprintf("Hello, %s", name), nil
	} else {
		return "Hello, world", nil
	}
}
