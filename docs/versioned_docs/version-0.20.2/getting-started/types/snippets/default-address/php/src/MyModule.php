<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultAddress;
use Dagger\Container;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function version(
        #[DefaultAddress('alpine:latest')]
        Container $ctr,
    ): string {
        return $ctr->withExec(['cat', '/etc/alpine-release'])->stdout();
    }
}
