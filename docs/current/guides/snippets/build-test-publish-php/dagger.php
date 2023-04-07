<?php
// include auto-loader
include 'vendor/autoload.php';

use GraphQL\Client;

class DaggerPipeline {

  private $client;

  // constructor
  public function __construct() {
    // initialize client with
    // endpoint from environment
    $sessionPort = getenv('DAGGER_SESSION_PORT') or throw new Exception("DAGGER_SESSION_PORT doesn't exist");
    $sessionToken = getenv('DAGGER_SESSION_TOKEN') or throw new Exception("DAGGER_SESSION_TOKEN doesn't exist");
    $this->client = new Client(
      'http://127.0.0.1:' . $sessionPort . '/query',
      ['Authorization' => 'Basic ' . base64_encode($sessionToken . ':')]
    );
  }

  // build base image
  public function buildBaseImage() {
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

    // build runtime image
    // install tools and PHP extensions
    // configure Apache webserver root and rewriting
    $runtimeQuery = <<<QUERY
    query {
      container {
        from(address: "php:8.2-apache-buster") {
          withExec(args: ["apt-get", "update"]) {
            withExec(args: ["apt-get", "install", "--yes", "git-core"]) {
              withExec(args: ["apt-get", "install", "--yes", "zip"]) {
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
    QUERY;
    $runtime = $this->executeQuery($runtimeQuery);

    // add application source code
    // set file permissions
    // set environment variables
    $appQuery = <<<QUERY
    query {
      container (id: "$runtime") {
        withMountedDirectory(path: "/mnt", source: "$sourceDir") {
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

  // build image for testing
  public function buildTestImage() {
    // build base image
    $image = $this->buildBaseImage();

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

  // build image for production
  public function buildProductionImage() {
    // build base image
    $image = $this->buildBaseImage();

    // set production-specific variables
    $appProductionQuery = <<<QUERY
    query {
      container (id: "$image") {
        withEnvVariable(name: "APP_DEBUG", value: "false") {
          withEnvVariable(name: "APP_NAME", value: "Laravel with Dagger") {
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

  // run unit tests
  public function testImage($image) {
    // create database service container
    $dbQuery = <<<QUERY
    query {
      container {
        from(address: "mariadb:10.11.2") {
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

  // publish image to registry
  public function publishImage($image) {
    // retrieve registry credentials from host environment
    $registryUsername = getenv("REGISTRY_USERNAME");
    $registryPassword = getenv("REGISTRY_PASSWORD");

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
        withRegistryAuth(address: "docker.io", username: "$registryUsername", secret: "$registryPasswordSecret") {
          publish(address: "$registryUsername/laravel-dagger")
        }
      }
    }
    QUERY;
    $address = $this->executeQuery($publishQuery);
    return $address;
  }

  // utility function to run raw GraphQL queries
  // and recurse over result to return innermost leaf node
  private function executeQuery($query) {
    $response = $this->client->runRawQuery($query);
    $data = (array)($response->getData());
    foreach(new RecursiveIteratorIterator(
      new RecursiveArrayIterator($data), RecursiveIteratorIterator::LEAVES_ONLY) as $value) {
      $results[] = $value;
    }
    return $results[0];
  }

}

// run pipeline
try {
  $p = new DaggerPipeline();

  // build test image
  echo "Building test image..." . PHP_EOL;
  $testImage = $p->buildTestImage();
  echo "Test image built." . PHP_EOL;

  // test
  echo "Running tests in test image..." . PHP_EOL;
  $result = $p->testImage($testImage);
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
