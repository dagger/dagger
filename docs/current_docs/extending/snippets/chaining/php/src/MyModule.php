<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

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
            ->withEntrypoint(['cat', '/etc/os-release'])
            ->publish('ttl.sh/my-alpine');
    }
}
