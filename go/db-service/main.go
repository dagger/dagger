package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create a Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Database service used for application tests
	database := client.Container().From("postgres:15.2").
		WithEnvVariable("POSTGRES_PASSWORD", "test").
		WithExec([]string{"postgres"}).
		WithExposedPort(5432)

	// Project to test
	src := client.Host().Directory(".")

	// Run application tests
	out, err := client.Container().From("golang:1.20").
		WithServiceBinding("db", database).     // bind database with the name db
		WithEnvVariable("DB_HOST", "db").       // db refers to the service binding
		WithEnvVariable("DB_PASSWORD", "test"). // password set in db container
		WithEnvVariable("DB_USER", "postgres"). // default user in postgres image
		WithEnvVariable("DB_NAME", "postgres"). // default db name in postgres image
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "test"}). // execute go test
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Printf(out)
}
