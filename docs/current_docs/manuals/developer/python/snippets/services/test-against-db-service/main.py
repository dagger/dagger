from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def test(self) -> str:
        """Runs unit tests against a database service"""
        # get MariaDB base image
        mariadb = (
            dag.container()
            .from_("mariadb:10.11.2")
            .with_env_variable("MARIADB_USER", "user")
            .with_env_variable("MARIADB_PASSWORD", "password")
            .with_env_variable("MARIADB_DATABASE", "drupal")
            .with_env_variable("MARIADB_ROOT_PASSWORD", "root")
            .with_exposed_port(3306)
            .as_service()
        )

        # get Drupal base image
        # install additional dependencies
        drupal = (
            dag.container()
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
        # run kernel tests using PHPUnit
        return await (
            drupal.with_service_binding("db", mariadb)
            .with_env_variable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
            .with_env_variable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
            .with_workdir("/opt/drupal/web/core")
            .with_exec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
            .stdout()
        )
