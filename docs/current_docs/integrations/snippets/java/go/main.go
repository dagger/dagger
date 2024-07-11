package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *dagger.Directory) *dagger.File {
	return dag.Java().
		WithJdk("17").
		WithMaven("3.9.5").
		WithProject(source.WithoutDirectory("dagger")).
		Maven([]string{"package"}).
		File("target/spring-petclinic-3.2.0-SNAPSHOT.jar")
}

func (m *MyModule) Publish(ctx context.Context, source *dagger.Directory, version string, registryAddress string, registryUsername string, registryPassword *dagger.Secret, imageName string) (string, error) {

	return dag.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("eclipse-temurin:17-alpine").
		WithLabel("org.opencontainers.image.title", "Java with Dagger").
		WithLabel("org.opencontainers.image.version", version).
		WithFile("/app/spring-petclinic-3.2.0-SNAPSHOT.jar", m.Build(ctx, source)).
		WithEntrypoint([]string{"java", "-jar", "/app/spring-petclinic-3.2.0-SNAPSHOT.jar"}).
		WithRegistryAuth(registryAddress, registryUsername, registryPassword).
		Publish(ctx, fmt.Sprintf("%s/%s/%s", registryAddress, registryUsername, imageName))
}
