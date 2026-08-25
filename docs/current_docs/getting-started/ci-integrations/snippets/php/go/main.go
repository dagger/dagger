package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// return container image with application source code and dependencies
func (m *MyModule) Build(source *dagger.Directory) *dagger.Container {
	return dag.Container().
		From("php:8.2").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "--yes", "git-core", "zip", "curl"}).
		WithExec([]string{"sh", "-c", "curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer"}).
		WithDirectory("/var/www", source.WithoutDirectory("dagger")).
		WithWorkdir("/var/www").
		WithExec([]string{"chmod", "-R", "775", "/var/www"}).
		WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		}).
		WithExec([]string{"composer", "install"})
}

// return result of unit tests
func (m *MyModule) Test(ctx context.Context, source *dagger.Directory) (string, error) {
	return m.Build(source).
		WithExec([]string{"phpunit"}).
		Stdout(ctx)
}

// return address of published container image
func (m *MyModule) Publish(ctx context.Context, source *dagger.Directory, version string, registryAddress string, registryUsername string, registryPassword *dagger.Secret, imageName string) (string, error) {
	return m.Build(source).
		WithLabel("org.opencontainers.image.title", "PHP with Dagger").
		WithLabel("org.opencontainers.image.version", version).
		WithEntrypoint([]string{"php", "-S", "0.0.0.0:8080", "-t", "public"}).
		WithExposedPort(8080).
		WithRegistryAuth(registryAddress, registryUsername, registryPassword).
		Publish(ctx, fmt.Sprintf("%s/%s/%s", registryAddress, registryUsername, imageName))
}
