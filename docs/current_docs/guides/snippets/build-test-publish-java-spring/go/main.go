package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *Directory) (string, error) {

	mariadb := dag.Mariadb().Serve(MariadbServeOpts{Version: "10.11.2", DbName: "petclinic"})

	// TODO: this doesn't work because the docker CLI isn't installed. And
	// installing it just gives us a broken podman CLI. Whee.
	//
	// Can we just disable the tests that use Docker?
	// dockerd := dag.Docker().Engine()

	app := dag.Java().
		WithJdk("17").
		WithMaven("3.9.5").
		WithProject(source.WithoutDirectory("dagger")).
		Container().
		WithServiceBinding("db", mariadb).
		WithEnvVariable("MYSQL_URL", "jdbc:mysql://db/petclinic").
		WithEnvVariable("MYSQL_USER", "root").
		WithEnvVariable("MYSQL_PASS", "").
		// TODO: this gets us kinda far, but we still don't have the 'docker' CLI installed
		// WithServiceBinding("docker", dockerd).
		// WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
		// TODO: this is fool's gold, it just gives us a broken podman CLI
		// WithExec([]string{"microdnf", "install", "docker"}).
		WithExec([]string{"mvn", "-Dspring.profiles.active=mysql", "clean", "package"})

	deploy := dag.Container().
		From("eclipse-temurin:17-alpine").
		WithDirectory("/app", app.Directory("./target")).
		WithEntrypoint([]string{"java", "-jar", "-Dspring.profiles.active=mysql", "/app/spring-petclinic-3.0.0-SNAPSHOT.jar"})

	address, err := deploy.Publish(ctx, "ttl.sh/myapp")
	if err != nil {
		return "", err
	}
	return address, nil
}
