<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function foo(): Container
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->terminal()
            ->withExec(["sh", "-c", "echo hello world > /foo"])
            ->terminal();
    }
}
