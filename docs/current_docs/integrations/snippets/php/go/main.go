package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

// return container image with application source code and dependencies
func (m *MyModule) Build(source *Directory) *Container {
	return dag.Container().
		From("php:8.2-apache-buster").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "--yes", "git-core", "zip", "curl"}).
		WithExec([]string{"docker-php-ext-install", "pdo", "pdo_mysql", "mysqli"}).
		WithExec([]string{"sh", "-c", "sed -ri -e 's!/var/www/html!/var/www/public!g' /etc/apache2/sites-available/*.conf"}).
		WithExec([]string{"sh", "-c", "sed -ri -e 's!/var/www/!/var/www/public!g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf"}).
		WithExec([]string{"a2enmod", "rewrite"}).
		WithDirectory("/var/www", source.WithoutDirectory("dagger")).
		WithWorkdir("/var/www").
		WithExec([]string{"chown", "-R", "www-data:www-data", "/var/www"}).
		WithExec([]string{"chmod", "-R", "775", "/var/www"}).
		// uncomment this to use a custom entrypoint file
		//WithExec([]string{"chmod", "+x", "/var/www/docker-entrypoint.sh"}).
		WithExec([]string{"sh", "-c", "curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer"}).
		WithExec([]string{"composer", "install"})
}

// return result of unit tests
func (m *MyModule) Test(ctx context.Context, source *Directory) (string, error) {
	return m.Build(source).
		WithExec([]string{"./vendor/bin/phpunit"}).
		Stdout(ctx)
}

// return address of published container image
func (m *MyModule) Publish(ctx context.Context, source *Directory, version string, registryAddress string, registryUsername string, registryPassword *Secret, imageName string) (string, error) {
	image := m.Build(source).
		WithLabel("org.opencontainers.image.title", "PHP with Dagger").
		WithLabel("org.opencontainers.image.version", version)
		// uncomment this to use a custom entrypoint file
		// .WithEntrypoint([]string{"/var/www/docker-entrypoint.sh"})

	address, err := image.
		WithRegistryAuth(registryAddress, registryUsername, registryPassword).
		Publish(ctx, fmt.Sprintf("%s/%s", registryUsername, imageName))

	if err != nil {
		return "", err
	}
	return address, nil
}
