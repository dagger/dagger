package main

import "context"

type Dep struct{}

func (m *Dep) GetSource(ctx context.Context) string {
	return "hello"
}
