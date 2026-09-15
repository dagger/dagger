package main

import "context"

type Foo struct{}

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return dag.Test().ContainerEcho("yoyoyo").Stdout(ctx)
}
