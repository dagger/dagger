package main

import (
	"context"
	"dagger/oldid/internal/dagger"
)

type Oldid struct{}

func (m *Oldid) RoundTrip(ctx context.Context) (string, error) {
	id, err := dag.Container().From("alpine:3.22.1").ID(ctx)
	if err != nil {
		return "", err
	}
	return dag.LoadContainerFromID(dagger.ContainerID(id)).WithExec([]string{"echo", "ok"}).Stdout(ctx)
}
