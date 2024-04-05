package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *Directory) (string, error) {

	mariadb := dag.Mariadb().Serve(dagger.MariadbServeOpts{Version: "10.11.2", DbName: "petclinic"})

	//dockerd := dag.Docker().Engine()

	app := dag.Java().
		WithJdk("17").
		WithMaven("3.9.5").
		WithProject(source.WithoutDirectory("dagger")).
		Maven([]string{"-X", "-Dspring.profiles.active=mysql", "clean", "package"})

	build := app.WithServiceBinding("db", mariadb).
		//WithServiceBinding("docker", dockerd).
		WithEnvVariable("MYSQL_URL", "jdbc:mysql://db/petclinic").
		WithEnvVariable("MYSQL_USER", "root").
		WithEnvVariable("MYSQL_PASS", "")

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
