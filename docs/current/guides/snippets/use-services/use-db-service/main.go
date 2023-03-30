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

	// get MariaDB base image
	mariadb := client.Container().
		From("mariadb:10.11.2").
		WithEnvVariable("MARIADB_USER", "user").
		WithEnvVariable("MARIADB_PASSWORD", "password").
		WithEnvVariable("MARIADB_DATABASE", "drupal").
		WithEnvVariable("MARIADB_ROOT_PASSWORD", "root").
		WithExec(nil)

		// get PHP base image
		// add Composer and install dependencies
		// install PHP extensions for PDO and MySQL
	php := client.Container().
		From("php:8.1-fpm-alpine3.17").
		WithExec([]string{"apk", "add", "composer", "php81-dom", "php81-gd", "php81-pdo", "php81-tokenizer", "php81-session", "php81-simplexml", "php81-xmlwriter", "php81-xml"}).
		WithExec([]string{"docker-php-ext-install", "mysqli", "pdo", "pdo_mysql"})

	// create new Drupal project
	drupal := php.
		WithWorkdir("/tmp").
		WithExec([]string{"composer", "create-project", "drupal/recommended-project", "my-project"}).
		WithWorkdir("/tmp/my-project").
		WithExec([]string{"composer", "require", "drupal/core-dev", "--dev", "--update-with-all-dependencies"})

	// add service binding for MariaDB
	// run kernel tests using PHPUnit
	test, err := drupal.
		WithServiceBinding("db", mariadb).
		WithEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal").
		WithEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled").
		WithWorkdir("/tmp/my-project/web/core").
		WithExec([]string{"../../vendor/bin/phpunit", "-v", "--group", "KernelTests"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(test)
}
