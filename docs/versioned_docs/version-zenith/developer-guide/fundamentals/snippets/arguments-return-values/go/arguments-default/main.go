package main

import (
	"context"
	"fmt"
)

type HelloWorld struct {}

func (m *HelloWorld) Hello(
	ctx context.Context,
	// +optional
	// +default="world"
	name string,
) (string, error) {
	return fmt.Sprintf("Hello, %s", name), nil
}
