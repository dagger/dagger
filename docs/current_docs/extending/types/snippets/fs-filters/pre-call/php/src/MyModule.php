<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject, Ignore};
use Dagger\{Container, Directory};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function foo(
        #[Ignore('*', '!**/*.php', '!composer.json')]
        Directory $source,
    ): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $source)
            ->sync();
    }
}
