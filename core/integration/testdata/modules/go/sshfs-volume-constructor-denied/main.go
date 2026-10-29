package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() *dagger.Volume {
	return dag.SSHFSVolume("sshfs://git@example.com/data", nil)
}
