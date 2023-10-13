package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

	if err != nil {
		panic(err)
	}
	defer client.Close()

	// expose host service on port 3306
	hostSrv := client.Host().Service([]dagger.PortForward{
		{Frontend: 3306, Backend: 3306},
	})

	// create MariaDB container
	// with host service binding
	// execute SQL query on host service
	out, err := client.Container().
		From("mariadb:10.11.2").
		WithServiceBinding("db", hostSrv).
		WithExec([]string{"/usr/bin/mysql", "--user=", "root", "--password=", "secret", "-e", "SELECT * FROM mysql.user"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)

}
