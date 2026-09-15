package main

import (
	_ "embed"

	"dagger/relayer/internal/dagger"
)

type Relayer struct{}

//go:embed relay/main.go
var relayMain string

func (m *Relayer) Service() *dagger.Service {
	return dag.Container().
		From("golang:1.26-alpine").
		WithWorkdir("/srv").
		WithNewFile("main.go", relayMain).
		WithDefaultArgs([]string{"go", "run", "main.go"}).
		WithExposedPort(80).
		AsService()
}
