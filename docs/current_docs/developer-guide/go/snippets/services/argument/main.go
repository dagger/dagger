package main

import (
	"context"
)

type MyModule struct{}

// sends a query to a MariaDB service received as input and returns the response
func (m *MyModule) UserList(ctx context.Context, svc *Service) (string, error) {
	out, err := dag.Container().
		From("mariadb:10.11.2").
		WithServiceBinding("db", svc).
		WithExec([]string{"/bin/sh", "-c", "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT Host, User FROM mysql.user'"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	return out, nil
}
