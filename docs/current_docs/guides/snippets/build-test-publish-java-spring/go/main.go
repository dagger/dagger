package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *Directory) (string, error) {

	mariadb := dag.Container().
		From("mariadb:10.11.2").
		WithEnvVariable("MARIADB_USER", "petclinic").
		WithEnvVariable("MARIADB_PASSWORD", "petclinic").
		WithEnvVariable("MARIADB_DATABASE", "petclinic").
		WithEnvVariable("MARIADB_ROOT_PASSWORD", "root").
		WithExposedPort(3306).
		AsService()

	app := dag.Java().
		WithJdk("17").
		WithMaven("3.9.5").
		WithProject(source.WithoutDirectory("dagger")).
		Maven([]string{"-X", "-Dspring.profiles.active=mysql", "clean", "package"})

	build := app.WithServiceBinding("db", mariadb).
		WithEnvVariable("MYSQL_URL", "jdbc:mysql://db/petclinic").
		WithEnvVariable("MYSQL_USER", "petclinic").
		WithEnvVariable("MYSQL_PASS", "petclinic")

	deploy := dag.Container().
		From("eclipse-temurin:17-alpine").
		WithDirectory("/app", build.Directory("./target")).
		WithEntrypoint([]string{"java", "-jar", "-Dspring.profiles.active=mysql", "/app/spring-petclinic-3.0.0-SNAPSHOT.jar"})

	address, err := deploy.Publish(ctx, "ttl.sh/myapp")
	if err != nil {
		return "", err
	}
	return address, nil
}
