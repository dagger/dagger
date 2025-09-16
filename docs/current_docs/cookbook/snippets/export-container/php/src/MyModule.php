<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    #[Doc('Return a container')]
    public function base(): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(['mkdir', '/src'])
            ->withExec(['touch', '/src/foo', '/src/bar']);
    }
}
