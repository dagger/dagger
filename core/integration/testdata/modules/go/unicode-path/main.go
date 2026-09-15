package main

import (
	"context"
)

type Test struct{}

func (m *Test) Hello(ctx context.Context) string {
	return "hello"
}
