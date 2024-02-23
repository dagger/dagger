package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Hello(ctx context.Context) (string, error) {
	return "Hello, world", nil
}
