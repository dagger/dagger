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
    public function container(): Container {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->terminal()
            ->withExec(['sh', '-c', 'echo hello world > /foo && cat /foo'])
            ->terminal();
    }
}
