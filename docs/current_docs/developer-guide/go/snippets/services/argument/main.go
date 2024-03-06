package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) UserList(ctx context.Context, hostService *Service) string {
	out, err := dag.Container().
		From("mariadb:10.11.2").
		WithServiceBinding("db", hostService).
		WithExec([]string{"/bin/sh", "-c", "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT Host, User FROM mysql.user'"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}
	return out
}
