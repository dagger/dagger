package main

import (
	"context"

	"main/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) OsInfo(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.
		WithExec([]string{"uname", "-a"}).
		Stdout(ctx)
}
