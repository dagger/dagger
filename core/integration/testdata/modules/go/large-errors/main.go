package main

import "context"

type Test struct{}

func (m *Test) RunNoisy(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine:3.22.1").
		WithExec([]string{"sh", "-c", `
			for i in $(seq 100); do
				for j in $(seq 1024); do
					echo -n x
					echo -n y >/dev/stderr
				done
				echo
			done
			exit 42
		`}).
		Sync(ctx)
	return err
}
