import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {

    // get MariaDB base image
    const mariadb = client
      .container()
      .from("mariadb:10.11.2")
      .withEnvVariable("MARIADB_USER", "user")
      .withEnvVariable("MARIADB_PASSWORD", "password")
      .withEnvVariable("MARIADB_DATABASE", "drupal")
      .withEnvVariable("MARIADB_ROOT_PASSWORD", "root")
      .withExec([]);

    // get PHP base image
    // add Composer and install dependencies
    // install PHP extensions for PDO and MySQL
    const php = client
      .container()
      .from("php:8.1-fpm-alpine3.17")
      .withExec(["apk", "add", "composer", "php81-dom", "php81-gd", "php81-pdo", "php81-tokenizer", "php81-session", "php81-simplexml", "php81-xmlwriter", "php81-xml"])
      .withExec(["docker-php-ext-install", "mysqli", "pdo", "pdo_mysql"]);

    // create new Drupal project
    const drupal = php
      .withWorkdir("/tmp")
      .withExec(["composer", "create-project", "drupal/recommended-project", "my-project"])
      .withWorkdir("/tmp/my-project")
      .withExec(["composer", "require", "drupal/core-dev", "--dev", "--update-with-all-dependencies"]);

    // add service binding for MariaDB
    // run unit tests using PHPUnit
    const test = await drupal
      .withServiceBinding("db", mariadb)
      .withEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
      .withEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
      .withWorkdir("/tmp/my-project/web/core")
      .withExec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
      .stdout();

    // print ref
    console.log(test);

  }, { LogOutput: process.stderr }
);
