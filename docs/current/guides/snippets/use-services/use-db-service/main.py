import sys

import anyio
import dagger

async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # get MariaDB base image
        mariadb = (
            client
            .container()
            .from_("mariadb:10.11.2")
            .with_env_variable("MARIADB_USER", "user")
            .with_env_variable("MARIADB_PASSWORD", "password")
            .with_env_variable("MARIADB_DATABASE", "drupal")
            .with_env_variable("MARIADB_ROOT_PASSWORD", "root")
            .with_exec([])
        )

        # get PHP base image
        # add Composer and install dependencies
        # install PHP extensions for PDO and MySQL
        php = (
            client
            .container()
            .from_("php:8.1-fpm-alpine3.17")
            .with_exec(["apk", "add", "composer", "php81-dom", "php81-gd", "php81-pdo", "php81-tokenizer", "php81-session", "php81-simplexml", "php81-xmlwriter", "php81-xml"])
            .with_exec(["docker-php-ext-install", "mysqli", "pdo", "pdo_mysql"])
        )

        # create new Drupal project
        drupal = (
            php
            .with_workdir("/tmp")
            .with_exec(["composer", "create-project", "drupal/recommended-project", "my-project"])
            .with_workdir("/tmp/my-project")
            .with_exec(["composer", "require", "drupal/core-dev", "--dev", "--update-with-all-dependencies"])
        )

        # add service binding for MariaDB
        # run unit tests using PHPUnit
        test = await (
            drupal
            .with_service_binding("db", mariadb)
            .with_env_variable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
            .with_env_variable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
            .with_workdir("/tmp/my-project/web/core")
            .with_exec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
            .stdout()
        )

    # print ref
    print(test)

anyio.run(main)
