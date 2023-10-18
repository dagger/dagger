---
slug: /128409/build-test-publish-php
displayed_sidebar: "current"
category: "guides"
tags: ["php", "laravel"]
authors: ["Vikram Vaswani"]
date: "2023-04-17"
---

# Build, Test and Publish a Laravel Web Application with Dagger

## Introduction

Dagger SDKs are currently available for [Go](../sdk/go/), [Node.js](../sdk/nodejs/) and [Python](../sdk/python/) and make it easy to develop CI/CD pipelines in those languages. However, even if you're using a different language, you can still use Dagger via the [Dagger GraphQL API](../api/). The Dagger GraphQL API is a unified interface for programming the Dagger Engine that can be accessed and used by any standards-compliant GraphQL client.

This tutorial demonstrates the above by using PHP with a PHP-based GraphQL client to continuously build, test and publish a Laravel Web application using Dagger. You will learn how to:

- Create a custom client for the Dagger GraphQL API in PHP
- Connect to the Dagger GraphQL API and run GraphQL queries
- Create a Dagger pipeline to:
  - Build a container image of your Laravel application with all required tools and dependencies
  - Run unit tests for your Laravel application image
  - Publish the final application image to Docker Hub
- Run the Dagger pipeline locally using the Dagger CLI

:::tip
GraphQL has a [large and growing list of client implementations](https://graphql.org/code/#language-support) for over 20 languages.
:::

## Requirements

This tutorial assumes that:

- You have a basic understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- You have a PHP development environment with PHP 8.2.x and the Composer package manager installed. If not, [install PHP](https://www.php.net/downloads.php) and [install Composer](https://getcomposer.org/doc/00-intro.md).
- You have Docker installed and running in your development environment. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have a Docker Hub account. If not, [register for a free Docker Hub account](https://hub.docker.com/signup).
- You have a [Laravel](https://laravel.com/) 10.x Web application with a Docker entrypoint script for startup operations, such as running database migrations. If not, follow the steps in Appendix A to [create a skeleton Laravel Web application and entrypoint script](#appendix-a-create-a-laravel-web-application).

:::info
This tutorial assumes a Laravel Web application, but the steps and code samples described below can easily be adapted for use with any other PHP Web application.
:::

## Step 1: Install a GraphQL client for PHP

The first step is to install a GraphQL client for PHP.  This tutorial uses the [php-graphql-client library](https://github.com/mghoneimy/php-graphql-client), available under the MIT License.

Add the client to your application manifest and install it as follows:

```shell
composer require gmostafa/php-graphql-client --with-all-dependencies
```

## Step 2: Create the Dagger pipeline

Within the application directory, create a new directory and file at `ci/dagger.php` and add the following code to it:

```php file=snippets/build-test-publish-php/dagger.php
```

This code listing consists of two parts:

- A `DaggerPipeline` class with methods encapsulating the Dagger pipeline operations: build, test and publish.
- A pipeline script invoking the various class methods.

The steps performed by the pipeline script are:

- Create a Dagger GraphQL API client
- Build a test image
- Run unit tests
- Build a production image
- Publish the image

These steps are visible in the following code extract:

```php
// run pipeline
try {
  $p = new DaggerPipeline();

  // build test image
  echo "Building test image..." . PHP_EOL;
  $testImage = $p->buildTestImage();
  echo "Test image built." . PHP_EOL;

  // test
  echo "Running tests in test image..." . PHP_EOL;
  $result = $p->runUnitTests($testImage);
  echo "Tests completed." . PHP_EOL;

  // build production image
  echo "Building production image..." . PHP_EOL;
  $prodImage = $p->buildProductionImage();
  echo "Production image built." . PHP_EOL;

  // publish
  echo "Publishing production image..." . PHP_EOL;
  $address = $p->publishImage($prodImage);
  echo "Production image published at: $address" . PHP_EOL;
} catch (Exception $e) {
  print_r($e->getMessage());
  exit;
}
```

If any of the steps produce an error (for example, due to a unit test failure), the pipeline will terminate.

The following sections describe these steps in more detail.

### Create a Dagger GraphQL API client

The `DaggerPipeline` class constructor initializes a new GraphQL client for the Dagger GraphQL API and assigns it as a class member, as shown in the following extract:

```php
class DaggerPipeline {
  // ...

  private $client;

  // constructor
  public function __construct() {
    // initialize client with
    // endpoint from environment
    $sessionPort = getenv('DAGGER_SESSION_PORT') or throw new Exception("DAGGER_SESSION_PORT environment variable must be set");
    $sessionToken = getenv('DAGGER_SESSION_TOKEN') or throw new Exception("DAGGER_SESSION_TOKEN environment variable must be set");
    $this->client = new Client(
      'http://127.0.0.1:' . $sessionPort . '/query',
      ['Authorization' => 'Basic ' . base64_encode($sessionToken . ':')]
    );
  }

  // ...
}
```

The API endpoint and the HTTP authentication token for the GraphQL client are not statically defined, they must be retrieved at run-time from the special `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` environment variables. This is explained in detail later.

### Build a test image

The `buildTestImage()` method builds an image of the application for testing. Internally, this method calls the `buildApplicationImage()` method, which in turn calls the `buildRuntimeImage()` method. Here's what these methods look like:

```php
class DaggerPipeline {
  // ...

  // build runtime image
  public function buildRuntimeImage() {
    // build runtime image
    // install tools and PHP extensions
    // configure Apache webserver root and rewriting
    $runtimeQuery = <<<QUERY
    query {
      container (platform: "linux/amd64") {
        from(address: "$this->phpImage") {
          withExec(args: ["apt-get", "update"]) {
            withExec(args: ["apt-get", "install", "--yes", "git-core"]) {
              withExec(args: ["apt-get", "install", "--yes", "zip"]) {
                withExec(args: ["apt-get", "install", "--yes", "curl"]) {
                  withExec(args: ["docker-php-ext-install", "pdo", "pdo_mysql", "mysqli"]) {
                    withExec(args: ["sh", "-c", "sed -ri -e 's!/var/www/html!/var/www/public!g' /etc/apache2/sites-available/*.conf"]) {
                      withExec(args: ["sh", "-c", "sed -ri -e 's!/var/www/!/var/www/public!g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf"]) {
                        withExec(args: ["a2enmod", "rewrite"]) {
                          id
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
    QUERY;
    $runtime = $this->executeQuery($runtimeQuery);
    return $runtime;
  }

  // build application image
  public function buildApplicationImage() {
    // get runtime image
    $runtime = $this->buildRuntimeImage();

    // get host working directory
    $sourceQuery = <<<QUERY
    query {
      host {
        directory (path: ".", exclude: ["vendor", "ci"]) {
          id
        }
      }
    }
    QUERY;
    $sourceDir = $this->executeQuery($sourceQuery);

    // add application source code
    // set file permissions
    // set environment variables
    $appQuery = <<<QUERY
    query {
      container (id: "$runtime") {
        withDirectory(path: "/mnt", directory: "$sourceDir") {
          withWorkdir(path: "/mnt") {
            withExec(args: ["cp", "-R", ".", "/var/www"]) {
              withExec(args: ["chown", "-R", "www-data:www-data", "/var/www"]) {
                withExec(args: ["chmod", "-R", "777", "/var/www/storage"]) {
                  withExec(args: ["chmod", "+x", "/var/www/docker-entrypoint.sh"]) {
                    id
                  }
                }
              }
            }
          }
        }
      }
    }
    QUERY;
    $app = $this->executeQuery($appQuery);

    // install Composer
    // add application dependencies
    $appWithDepsQuery = <<<QUERY
    query {
      container (id: "$app") {
        withExec(args: ["sh", "-c", "curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer"]) {
          withWorkdir(path: "/var/www") {
            withExec(args: ["composer", "install"]) {
              id
            }
          }
        }
      }
    }
    QUERY;
    $appWithDeps = $this->executeQuery($appWithDepsQuery);
    return $appWithDeps;
  }

  // ...
}
```

1. The `buildRuntimeImage()` method executes a GraphQL query to construct a runtime image. This runtime image consists of the PHP interpreter, Apache webserver, and required tools and extensions. It uses the `container.from()` method to initialize a new container from the `php:8.2-apache-buster` image. It then chains multiple `container.withExec()` methods to add tools, PHP extensions and Apache configuration to the image.
1. The `buildApplicationImage()` method uses the image produced by `buildRuntimeImage()` and executes three additional GraphQL queries:
    - The first query obtains a reference to the source code directory of the application on the host using the `host.directory()` API method.
    - The next query continues building the image. It uses the `container.withDirectory()` method to return the container with the source code directory written at `/mnt`. It then chains multiple `container.withExec()` methods to copy the application source code to the Apache webserver's filesystem, and set various file permissions and environment variables.
    - The final query installs Composer in the image and runs `composer install` to download all the required application dependencies.

:::info
GraphQL query resolution is triggered only when a leaf value (scalar) is requested. Dagger leverages this lazy evaluation model to optimize and parallelize pipelines for maximum speed and performance. This implies that the queries above are not actually executed until necessary to return output (such as the result of a command or an exit code) to the requesting client. [Learn more about lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::

Once the application image is constructed, control returns to the `buildTestImage()` method:

```php
class DaggerPipeline {
  // ...

  // build image for testing
  public function buildTestImage() {
    // build base image
    $image = $this->buildApplicationImage();

    // set test-specific variables
    $appTestQuery = <<<QUERY
    query {
      container (id: "$image") {
        withEnvVariable(name: "APP_DEBUG", value: "true") {
          withEnvVariable(name: "LOG_LEVEL", value: "debug") {
            id
          }
        }
      }
    }
    QUERY;
    $appTest = $this->executeQuery($appTestQuery);
    return $appTest;
  }

  // ...
}
```

The `buildTestImage()` method executes one additional GraphQL query to add test-specific configuration to the image. It activates Laravel's detailed error logging by setting the `APP_DEBUG` and `LOG_LEVEL` environment variables in the container, using the `container.withEnvVariable()` API method.

### Run unit tests

The `runUnitTests()` method accepts an image reference and runs unit tests in the image. Here's what it looks like:

```php
class DaggerPipeline {
  // ...

  // run unit tests
  public function runUnitTests($image) {
    // create database service container
    $dbQuery = <<<QUERY
    query {
      container {
        from(address: "$this->mariadbImage") {
          withEnvVariable(name: "MARIADB_DATABASE", value: "t_db") {
            withEnvVariable(name: "MARIADB_USER", value: "t_user") {
              withEnvVariable(name: "MARIADB_PASSWORD", value: "t_password") {
                withEnvVariable(name: "MARIADB_ROOT_PASSWORD", value: "root") {
                  withExposedPort(port: 3306) {
                    withExec(args: []) {
                      id
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
    QUERY;
    $db = $this->executeQuery($dbQuery);

    // bind database service to application image
    // set database credentials for application
    // run all PHPUnit tests
    $testQuery = <<<QUERY
    query {
      container (id: "$image") {
        withServiceBinding(alias: "mariadb", service: "$db") {
          withEnvVariable(name: "DB_HOST", value: "mariadb") {
            withEnvVariable(name: "DB_USERNAME", value: "t_user") {
              withEnvVariable(name: "DB_PASSWORD", value: "t_password") {
                withEnvVariable(name: "DB_DATABASE", value: "t_db") {
                  withWorkdir(path: "/var/www") {
                    withExec(args: ["./vendor/bin/phpunit", "-vv"]) {
                      stdout
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
    QUERY;
    $test = $this->executeQuery($testQuery);
    return $test;
  }

  // ...
}
```

The `runUnitTests()` method executes two GraphQL queries:

1. The first query initializes a database service container, against which the application's unit tests will be run. It uses the `container.from()` method to initialize a new container from the `mariadb:10.11.2` image. It then chains multiple `container.withEnvVariable()` methods to configure the database service, and the `container.withExposedPort()` method to ensure that the service is available before allowing clients access.
1. The second query uses the test image returned by the `buildTestImage()` method and adds a service binding for the database service to it using the `container.withServiceBinding()` API method. It then chains multiple `container.withEnvVariable()` methods to configure the database service credentials for the Laravel application. Finally, it uses the `container.withExec()` method to launch the PHPUnit test runner and return the output stream (the test summary).

:::tip
When creating the database service container, using the `Container.withExposedPort` field is important. Without this field, Dagger will start the service container and immediately allow access to the test runner, without waiting for the service to start listening. This can result in test failures if the test runner is unable to connect to the service. With this field, Dagger will wait for the service to be listening first before allowing the test runner access to it. [Learn more about service containers in Dagger](./757394-use-service-containers.md).
:::

### Build a production image

The `buildProductionImage()` method builds an image of the application for production. Internally, this method also calls the `buildApplicationImage()` method. Here's what it looks like:

```php
class DaggerPipeline {
  // ...

  // build image for production
  public function buildProductionImage() {
    // build base image
    $image = $this->buildApplicationImage();

    // set production-specific variables
    $appProductionQuery = <<<QUERY
    query {
      container (id: "$image") {
        withEnvVariable(name: "APP_DEBUG", value: "false") {
          withLabel(name: "org.opencontainers.image.title", value: "Laravel with Dagger") {
            withEntrypoint(args: "/var/www/docker-entrypoint.sh") {
              id
            }
          }
        }
      }
    }
    QUERY;
    $appProduction = $this->executeQuery($appProductionQuery);
    return $appProduction;
  }

  // ...
}
```

The `buildProductionImage()` method references the base image and executes one additional GraphQL query to add production-specific configuration to the image. More specifically, it turns off detailed application error messages for greater security using the `container.withEnvVariable()` API method. It also sets an [OpenContainer annotation](https://github.com/opencontainers/image-spec/blob/main/annotations.md#pre-defined-annotation-keys) for the container using the `container.withLabel()` API method.

### Publish the image

The `publishImage()` method accepts an image reference and publishes the corresponding image to Docker Hub. Here's what it looks like:

```php
class DaggerPipeline {
  // ...

  // publish image to registry
  public function publishImage($image) {
    // retrieve registry address and credentials from host environment
    $registryAddress = getenv("REGISTRY_ADDRESS", true) ?: "docker.io";
    $registryUsername = getenv("REGISTRY_USERNAME") or throw new Exception("REGISTRY_USERNAME environment variable must be set");
    $registryPassword = getenv("REGISTRY_PASSWORD") or throw new Exception("REGISTRY_PASSWORD environment variable must be set");
    $containerAddress = getenv("CONTAINER_ADDRESS");
    if (empty($containerAddress)) {
      $containerAddress = "$registryUsername/laravel-dagger";
    }

    // set registry password as Dagger secret
    $registryPasswordSecretQuery = <<<QUERY
    query {
      setSecret(name: "password", plaintext: "$registryPassword") {
        id
      }
    }
    QUERY;
    $registryPasswordSecret = $this->executeQuery($registryPasswordSecretQuery);

    // authenticate to registry
    // publish image
    $publishQuery = <<<QUERY
    query {
      container (id: "$image") {
        withRegistryAuth(address: "$registryAddress", username: "$registryUsername", secret: "$registryPasswordSecret") {
          publish(address: "$containerAddress")
        }
      }
    }
    QUERY;
    $address = $this->executeQuery($publishQuery);
    return $address;
  }

  // ...
}
```

The `publishImage()` method expects to source the registry credentials from the host environment. It defaults to `docker.io` for the registry address, although this can be overridden from the host environment. It uses PHP's `getenv()` method to retrieve these details and then executes two GraphQL queries:

1. The first query creates a Dagger secret to store the registry password, via the `setSecret()` API method.
1. The second query authenticates and publishes the image to the specified registry. It uses the `container.withRegistryAuth()` API method for authentication, and the `container.publish()` method for the publishing operation. The `container.publish()` method returns the address and hash for the published image.

:::tip
Using a Dagger secret for confidential information ensures that the information is never exposed in plaintext logs, in the filesystem of containers you're building, or in any cache. Dagger also automatically scrubs secrets from its various logs and output streams. This ensures that sensitive data does not leak - for example, in the event of a crash. [Learn more about secrets in Dagger](./723462-use-secrets.md).
:::

## Step 3: Run the Dagger pipeline

Configure the registry credentials using environment variable on the local host. Although you can use any registry, this guide assumes usage of Docker Hub. Replace the `USERNAME` and `PASSWORD` placeholders with your Docker Hub credentials.

```shell
export REGISTRY_USERNAME=USERNAME
export REGISTRY_PASSWORD=PASSWORD
```

{@include: ../partials/_run_api_client.md}

Run the pipeline as below:

```shell
dagger --silent run php ci/dagger.php
```

:::tip
For more detailed logs, remove the `--silent` option and add the `--debug` option to the `dagger run` command. [Learn more about the Dagger CLI](../cli/index.md).
:::

This command:

- initializes a new Dagger Engine session;
- sets the `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` environment variables;
- executes the PHP pipeline script in that session.

The pipeline script, in turn, initializes a new `DaggerPipeline` object, whose constructor:

- reads the above environment variables;
- creates a new GraphQL API client;
- connects to the API endpoint specified in the `DAGGER_SESSION_PORT` environment variable;
- sets an HTTP Basic authentication token with `DAGGER_SESSION_TOKEN`.

The remainder of the pipeline is then executed as described in the previous section. Here is an example of the output from a successful run:

```shell
Building test image...
Test image built.
Running tests in test image...
Tests completed.
Building production image...
Production image built.
Publishing production image...
Production image published at: docker.io/.../laravel-dagger@sha256:aa43...
```

Here is an example of the output from an unsuccessful run due to a failed unit test:

```shell
Building test image...
Test image built.
Running tests in test image...
process "docker-php-entrypoint ./vendor/bin/phpunit -vv" did not complete successfully: exit code: 2
Stdout:
...
2) Tests\Feature\ProfileTest::test_profile_information_can_be_updated
Failed asserting that two strings are identical.
--- Expected
+++ Actual
@@ @@
-'Test User'
+'Brooke McDermott'

/var/www/tests/Feature/ProfileTest.php:41
/var/www/vendor/laravel/framework/src/Illuminate/Foundation/Testing/TestCase.php:173

FAILURES!
Tests: 24, Assertions: 52, Failures: 2.
Stderr:
```

Test the published image by executing the commands below (replace the `USERNAME` placeholder with your registry username) and then browse to `http://localhost` to see the Laravel application running (by default, on port 80 of the Docker host):

```shell
docker run --rm --detach -p 3306:3306 --name my-mariadb --env MARIADB_USER=user --env MARIADB_PASSWORD=password --env MARIADB_DATABASE=laravel --env MARIADB_ROOT_PASSWORD=secret  mariadb:10.11.2

docker run --rm --detach --net=host --name my-app -e DB_HOST="127.0.0.1" -e DB_USERNAME="user" -e DB_PASSWORD="password" -e DB_DATABASE="laravel" USERNAME/laravel-dagger:latest
```

## Conclusion

Dagger SDKs are currently available for Go, Node.js and Python, but that doesn't mean you're restricted to only these languages when defining your Dagger CI/CD pipelines. You can use any standards-compatible GraphQL client to interact with the Dagger Engine from your favorite programming language.

This tutorial demonstrated by creating a PHP-based Dagger pipeline to build, test and publish a Laravel Web application. A similar approach can be followed for any PHP application, or in any other programming language with a GraphQL client implementation.

Use the [API Reference](https://docs.dagger.io/api/reference) and the [CLI Reference](../cli/979595-reference.md) to learn more about the Dagger GraphQL API and the Dagger CLI respectively.

## Appendix A: Create a Laravel Web application

This tutorial assumes that you have a Laravel 10.x Web application. If not, follow the steps below to create one.

:::info
The Laravel CLI requires `npm` for some of its operations, so the following steps assume that you have Node.js 18.x and `npm` installed. If you don't, [install Node.js](https://nodejs.org/en/download/) and [install `npm`](https://docs.npmjs.com/downloading-and-installing-node-js-and-npm) before proceeding.
:::

1. Install the Laravel CLI:

  ```shell
  composer global require laravel/installer
  ```

1. Add the Composer vendor directory to your system path:

  ```shell
  export PATH=$PATH:$HOME/.composer/vendor/bin
  ```

1. Create a skeleton application with the [Laravel Breeze scaffolding](https://laravel.com/docs/10.x/starter-kits#laravel-breeze):

  ```shell
  laravel new --breeze --stack=blade --phpunit --no-interaction myapp
  ```

1. Create a Docker entrypoint script named `docker-entrypoint.sh` in the application directory to handle startup operations, such as running database migrations:

  ```shell
  cat > docker-entrypoint.sh <<EOF
  #!/bin/bash
  php artisan migrate
  apache2-foreground
  EOF
  ```
