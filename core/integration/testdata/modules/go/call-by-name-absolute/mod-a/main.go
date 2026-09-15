package main

import "context"

type ModA struct{}

func (m *ModA) Fn(ctx context.Context) string {
	return "hi from mod-a"
}
