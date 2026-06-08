package main

import (
	"context"
)

type Test struct{}

func (m *Test) TraceURL(ctx context.Context) (string, error) {
	return dag.Cloud().TraceURL(ctx)
}
