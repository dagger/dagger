package main

import "context"

type ModB struct{}

func (m *ModB) Fn(ctx context.Context) string {
	return "hi from mod-b"
}
