package main

import (
	"dagger/servicer/internal/dagger"
)

type Servicer struct{}

func (m *Servicer) EchoSvc() *dagger.Service {
	return dag.Container().
		From("alpine:3.20").
		WithExec([]string{"apk", "add", "socat"}).
		WithExposedPort(1234).
		// echo server, writes what it reads
		WithDefaultArgs([]string{"socat", "tcp-l:1234,fork", "exec:/bin/cat"}).
		AsService()
}
