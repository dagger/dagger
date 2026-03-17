package io.dagger.modules.mymodule;
import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /** Run unit tests against a database service */
  @Function
  public String test() throws ExecutionException, DaggerQueryException, InterruptedException {
    Service mariadb =
        dag().container()
            .from("mariadb:10.11.2")
            .withEnvVariable("MARIADB_USER", "user")
            .withEnvVariable("MARIADB_PASSWORD", "password")
            .withEnvVariable("MARIADB_DATABASE", "drupal")
            .withEnvVariable("MARIADB_ROOT_PASSWORD", "root")
            .withExposedPort(3306)
            .asService(new Container.AsServiceArguments().withUseEntrypoint(true));

    // get Drupal base image
    // install additional dependencies
    Container drupal =
        dag().container()
            .from("drupal:10.0.7-php8.2-fpm")
            .withExec(
                List.of(
                    "composer",
                    "require",
                    "drupal/core-dev",
                    "--dev",
                    "--update-with-all-dependencies"));

    // add service binding for MariaDB
    // run kernel test using PHPUnit
    return drupal
        .withServiceBinding("db", mariadb)
        .withEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
        .withEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
        .withWorkdir("/opt/drupal/web/core")
        .withExec(List.of("../../vendor/bin/phpunit", "-v", "--group", "KernelTests"))
        .stdout();
  }
}