<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Attribute\ListOfType;
use Dagger\Container;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Return a container with a filtered directory')]
    public function copyDirectoryWithExclusions(
        #[Doc('source directory')]
        Directory $source,
        #[Doc('exclusion pattern')]
        #[ListOfType('string')]
        ?array $exclude = null
    ): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $source, $exclude);
    }
}
