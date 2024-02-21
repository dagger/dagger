package main

import (
	"context"
)

type HelloWorld struct{}

func (m *HelloWorld) Hello(ctx context.Context) (string, error) {
	return "Hello, world", nil
}
