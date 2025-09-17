package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) UserList(
	ctx context.Context,
	svc *dagger.Service,
) (string, error) {
	return dag.Container().
		From("mariadb:10.11.2").
		WithServiceBinding("db", svc).
		WithExec([]string{"/usr/bin/mysql", "--user=root", "--password=secret", "--host=db", "-e", "SELECT Host, User FROM mysql.user"}).
		Stdout(ctx)
}
