import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get MariaDB base image
        mariadb = (
            client.container()
            .from_("mariadb:10.11.2")
            .with_env_variable("MARIADB_USER", "user")
            .with_env_variable("MARIADB_PASSWORD", "password")
            .with_env_variable("MARIADB_DATABASE", "drupal")
            .with_env_variable("MARIADB_ROOT_PASSWORD", "root")
            .with_exposed_port(3306)
            .service()
        )

        # get Drupal base image
        # install additional dependencies
        drupal = (
            client.container()
            .from_("drupal:10.0.7-php8.2-fpm")
            .with_exec(
                [
                    "composer",
                    "require",
                    "drupal/core-dev",
                    "--dev",
                    "--update-with-all-dependencies",
                ]
            )
        )

        # add service binding for MariaDB
        # run unit tests using PHPUnit
        test = await (
            drupal.with_service_binding("db", mariadb)
            .with_env_variable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
            .with_env_variable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
            .with_workdir("/opt/drupal/web/core")
            .with_exec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
            .stdout()
        )

    print(test)


anyio.run(main)
