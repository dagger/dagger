package main

import "context"

type Test struct{}

func (m *Test) Fn(ctx context.Context) string {
	return "hi from dep"
}
