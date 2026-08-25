<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    // Build an application using cached dependencies
    #[DaggerFunction]
    public function build(
        // source code location
        Directory $source,
    ): Container {
        return dag()
            ->container()
            ->from('php:8.3-cli')
            ->withDirectory('/src', $source)
            ->withWorkdir('/src')
            ->withMountedCache('/root/.composer', dag()->cacheVolume('composer'))
            ->withExec(['composer', 'install']);
    }
}
