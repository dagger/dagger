<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function clone(string $repository, string $ref): Container
    {
        $repoDir = dag()->git($repository)->ref($ref)->tree();

        return dag()
            ->container()
            ->from('alpine:latest')
            ->withDirectory('/src', $repoDir);
    }
}
