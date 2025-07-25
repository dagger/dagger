<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction, Doc};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Run unit tests against a database service')]
    public function test(): string
    {
        $mariadb = dag()
            ->container()
            ->from('mariadb:10.11.2')
            ->withEnvVariable('MARIADB_USER', 'user')
            ->withEnvVariable('MARIADB_PASSWORD', 'password')
            ->withEnvVariable('MARIADB_DATABASE', 'drupal')
            ->withEnvVariable('MARIADB_ROOT_PASSWORD', 'root')
            ->withExposedPort(3306)
            ->asService(useEntrypoint: true);


        // get Drupal base image
        // install additional dependencies
        $drupal = dag()
            ->from('drupal:10.0.7-php8.2-fpm')
            ->withExec([
                'composer',
                'require',
                'drupal/core-dev',
                '--dev',
                '--update-with-all-dependencies',
            ]);

        // add service binding for MariaDB
        // run kernel tests using PHPUnit
        return $drupal
            ->withServiceBinding('db', mariadb)
            ->withEnvVariable('SIMPLETEST_DB', 'mysql://user:password@db/drupal')
            ->withEnvVariable('SYMFONY_DEPRECATIONS_HELPER', 'disabled')
            ->withWorkdir('/opt/drupal/web/core')
            ->withExec(['../../vendor/bin/phpunit', '-v', '--group', 'KernelTests'])
            ->stdout();
    }
}
