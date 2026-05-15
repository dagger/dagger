package main

import "dagger/dep/internal/dagger"

type Dep struct{}

func (m *Dep) Fn(ctr *dagger.Container, sock *dagger.Socket) *dagger.Container {
	return ctr.WithUnixSocket("/var/run/host.sock", sock)
}
