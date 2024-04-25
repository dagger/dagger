package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *Directory) *File {
	return dag.Java().
		WithJdk("17").
		WithMaven("3.9.5").
		WithProject(source.WithoutDirectory("dagger")).
		Maven([]string{"package"}).
		File("target/spring-petclinic-3.2.0-SNAPSHOT.jar")
}

func (m *MyModule) Publish(ctx context.Context, source *Directory, version string, registryAddress string, registryUsername string, registryPassword *Secret, imageName string) (string, error) {

	return dag.Container().
		From("eclipse-temurin:17-alpine").
		WithLabel("org.opencontainers.image.title", "Java with Dagger").
		WithLabel("org.opencontainers.image.version", version).
		WithFile("/app/spring-petclinic-3.2.0-SNAPSHOT.jar", m.Build(ctx, source)).
		WithEntrypoint([]string{"java", "-jar", "/app/spring-petclinic-3.2.0-SNAPSHOT.jar"}).
		WithRegistryAuth(registryAddress, registryUsername, registryPassword).
		Publish(ctx, fmt.Sprintf("%s/%s/%s", registryAddress, registryUsername, imageName))
}
