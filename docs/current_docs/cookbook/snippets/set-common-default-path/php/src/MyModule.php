<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\ReturnsListOfType;
use Dagger\Directory;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function __construct(
        #[DefaultPath('.')]
        public Directory $source
    ) {
    }

    #[DaggerFunction]
    #[ReturnsListOfType('string')]
    public function foo(): array
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withMountedDirectory('/app', $this->source)
            ->directory('/app')
            ->entries();
    }
}
