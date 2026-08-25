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
    // Run a build with cache invalidation
    #[DaggerFunction]
    public function build(): string
    {
        return dag()
            ->container()
            ->from('alpine')
            // comment out the line below to see the cached date output
            ->withEnvVariable('CACHEBUSTER', date(DATE_RFC2822))
            ->withExec(['date'])
            ->stdout();
    }
}
