<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
final class PhpSdkDev
{
    #[DaggerFunction('Run integration tests from source directory')]
    public function integrationTests(Directory $source): Container {
        return $this->base($source)->withExec(
                ['./vendor/bin/phpunit', '--group=integration'],
                experimentalPrivilegedNesting: true,
            );
     }

    #[DaggerFunction('Run unit tests from source directory')]
    public function unitTests(Directory $source): Container {
        return $this->base($source)->withExec(
            ['./vendor/bin/phpunit', '--group=unit']
        );
    }

    #[DaggerFunction('Run unit tests from source directory')]
    public function tests(Directory $source): Container {
        return $this->base($source)->withExec(
                ['./vendor/bin/phpunit'],
                experimentalPrivilegedNesting: true,
            );
    }

    private function base(Directory $source): Container
    {
        return dag()
            ->container()
            ->from('php:8.3-cli-alpine')
            ->withFile('/usr/bin/composer', dag()
                ->container()
                ->from('composer:2')
                ->file('/usr/bin/composer'))
            ->withMountedCache('/root/.composer', dag()
                ->cacheVolume('composer-php:8.3-cli-alpine'))
            ->withEnvVariable('COMPOSER_HOME', '/root/.composer')
            ->WithEnvVariable('COMPOSER_ALLOW_SUPERUSER', '1')
            ->withMountedDirectory('/src/sdk/php', $source)
            ->withWorkdir('/src/sdk/php')
            ->withExec(['composer', 'install']);
    }
}
