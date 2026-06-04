<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};
use Dagger\Container;

use function Dagger\dag;

#[DaggerObject]
class MyModule
{
    private Container $ctr;

    #[DaggerFunction]
    public function __construct(
        ?Container $ctr,
    ) {
        $this->ctr = $ctr ?? dag()->container()->from('alpine:3.14.0');
    }

    #[DaggerFunction]
    public function version(): string
    {
        return $this->ctr
            ->withExec(['/bin/sh', '-c', 'cat /etc/os-release | grep VERSION_ID'])
            ->stdout();
    }
}
