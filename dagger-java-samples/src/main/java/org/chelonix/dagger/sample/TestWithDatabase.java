package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Container;
import org.chelonix.dagger.client.Dagger;

import java.util.List;

public class TestWithDatabase {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {

            // get MariaDB base image
            Container mariadb = client.container()
                    .from("mariadb:10.11.2")
                    .withEnvVariable("MARIADB_USER", "user")
                    .withEnvVariable("MARIADB_PASSWORD", "password")
                    .withEnvVariable("MARIADB_DATABASE", "drupal")
                    .withEnvVariable("MARIADB_ROOT_PASSWORD", "root")
                    .withExposedPort(3306);

            // get Drupal base image
            // install additional dependencies
            Container drupal = client.container()
                    .from("drupal:10.0.7-php8.2-fpm")
                    .withExec(List.of("composer", "require", "drupal/core-dev", "--dev", "--update-with-all-dependencies"));

            // add service binding for MariaDB
            // run kernel tests using PHPUnit
            String test = drupal
                    .withServiceBinding("db", mariadb)
                    .withEnvVariable("SIMPLETEST_DB", "mysql://user:password@db/drupal")
                    .withEnvVariable("SYMFONY_DEPRECATIONS_HELPER", "disabled")
                    .withWorkdir("/opt/drupal/web/core")
                    .withExec(List.of("../../vendor/bin/phpunit", "-v", "--group", "KernelTests"))
                    .stdout();

            System.out.println(test);
        }
    }
}

