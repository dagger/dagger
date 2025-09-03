<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Ignore;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    // Return a container with a filtered directory
    public function copyDirectoryWithExclusions(
        // source directory
        #[Ignore('*', '!**/*.md')]
        Directory $source,
    ): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $source);
    }
}
