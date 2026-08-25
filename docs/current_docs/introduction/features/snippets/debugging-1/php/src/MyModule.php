<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function foo(): string
    {
        return dag()
            ->container()
            ->from('alpine:latest')
            ->withExec(['sh', '-c', 'echo hello world > /foo'])
            ->withExec(['cat', '/FOO']) // deliberate error
            ->stdout();
    }
}
