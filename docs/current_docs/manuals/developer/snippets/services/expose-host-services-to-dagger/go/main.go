package main

import (
	"context"
	"main/internal/dagger"
)

type MyModule struct{}

// Send a query to a MariaDB service and return the response
func (m *MyModule) UserList(
	ctx context.Context,
	// Host service
	svc *dagger.Service,
) (string, error) {
	return dag.Container().
		From("mariadb:10.11.2").
		WithServiceBinding("db", svc).
		WithExec([]string{"/usr/bin/mysql", "--user=root", "--password=secret", "--host=db", "-e", "SELECT Host, User FROM mysql.user"}).
		Stdout(ctx)
}
