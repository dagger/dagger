package main

import "context"

type Test struct{}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
