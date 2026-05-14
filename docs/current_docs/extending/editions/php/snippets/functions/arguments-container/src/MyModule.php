<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};
use Dagger\Container;

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function osInfo(Container $ctr): string
    {
        return $ctr->withExec(['uname', '-a'])->stdout();
    }
}
