import { connect, Client } from "@dagger.io/dagger"

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
      .withExposedPort(3306)
      .asService()

    // get Drupal base image
    // install additional dependencies
    const drupal = client
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
    // run unit tests using PHPUnit
    const test = await drupal
      .withServiceBinding("db", mariadb)
      .withEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
      .withEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
      .withWorkdir("/opt/drupal/web/core")
      .withExec(["../../vendor/bin/phpunit", "-v", "--group", "KernelTests"])
      .stdout()

    // print ref
    console.log(test)
  },
  { LogOutput: process.stderr },
)
