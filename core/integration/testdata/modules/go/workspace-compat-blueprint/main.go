package main

import "context"

type Hello struct{}

func (m *Hello) Greet(ctx context.Context) string {
	return "hello from blueprint"
}
