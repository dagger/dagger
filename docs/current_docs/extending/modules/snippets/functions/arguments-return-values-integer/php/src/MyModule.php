<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, DaggerFunction};

#[DaggerObject]
class MyModule
{
    #[DaggerFunction]
    public function addInteger(int $a, int $b): int
    {
        return $a + $b;
    }
}
