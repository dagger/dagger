package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Test(ctx context.Context) (string, error) {
	// get MariaDB base image
	mariadb := dag.Container().
		From("mariadb:10.11.2").
		WithEnvVariable("MARIADB_USER", "user").
		WithEnvVariable("MARIADB_PASSWORD", "password").
		WithEnvVariable("MARIADB_DATABASE", "drupal").
		WithEnvVariable("MARIADB_ROOT_PASSWORD", "root").
		WithExposedPort(3306).
		AsService()

	// get Drupal base image
	// install additional dependencies
	drupal := dag.Container().
		From("drupal:10.0.7-php8.2-fpm").
		WithExec([]string{"composer", "require", "drupal/core-dev", "--dev", "--update-with-all-dependencies"})

	// add service binding for MariaDB
	// run kernel tests using PHPUnit
	test, err := drupal.
		WithServiceBinding("db", mariadb).
		WithEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal").
		WithEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled").
		WithWorkdir("/opt/drupal/web/core").
		WithExec([]string{"../../vendor/bin/phpunit", "-v", "--group", "KernelTests"}).
		Stdout(ctx)

	if err != nil {
		return "", err
	}
	return test, nil
}
