package main

import "context"

type Dep struct{}

func (m *Dep) Fn(ctx context.Context) string {
	return "hi from dep1"
}
