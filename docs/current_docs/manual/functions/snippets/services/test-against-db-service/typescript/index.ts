import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Run unit tests against a database service
   */
  @func()
  async test(): Promise<string> {
    const mariadb = dag
      .container()
      .from("mariadb:10.11.2")
      .withEnvVariable("MARIADB_USER", "user")
      .withEnvVariable("MARIADB_PASSWORD", "password")
      .withEnvVariable("MARIADB_DATABASE", "drupal")
      .withEnvVariable("MARIADB_ROOT_PASSWORD", "root")
      .withExposedPort(3306)
      .asService()

    // get Drupal base image
    // install additional dependencies
    const drupal = dag
      .container()
      .from("drupal:10.0.7-php8.2-fpm")
      .withExec([
        "composer",
        "require",
        "drupal/core-dev",
        "--dev",
        "--update-with-all-dependencies",
      ])

    // add service binding for MariaDB
    // run kernel tests using PHPUnit
    return await drupal
      .withServiceBinding("db", mariadb)
      .withEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
      .withEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
      .withWorkdir("/opt/drupal/web/core")
      .withExec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
      .stdout()
  }
}
