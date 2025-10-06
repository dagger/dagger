<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    // Set a single environment variable in a container
    #[DaggerFunction]
    public function setEnvVar(): string
    {
        return dag()
            ->container()
            ->from('alpine')
            ->withEnvVariable('ENV_VAR', 'VALUE')
            ->withExec(['env'])
            ->stdout();
    }
}
